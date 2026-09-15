// Command api runs the Viatuta HTTP server.
//
// Everything here is startup wiring: load config, connect to the database,
// build the handler, serve, and shut down cleanly. Business logic lives in
// internal/ so that it can be tested without starting a server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wesishee/viatuta/internal/config"
	"github.com/wesishee/viatuta/internal/graph"
	"github.com/wesishee/viatuta/internal/httpapi"
	"github.com/wesishee/viatuta/internal/store"
)

func main() {
	// main() does nothing but call run() and translate an error into an
	// exit code. Keeping the real work in a function that returns an error
	// means deferred cleanup actually runs — os.Exit skips defers.
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := cfg.NewLogger()
	slog.SetDefault(logger)

	// notifyContext gives us a context that is cancelled when the user
	// presses Ctrl-C or the platform sends SIGTERM. Everything downstream
	// watches this context, so shutdown propagates automatically.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("connecting to database")
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := store.CheckPostGIS(ctx, pool); err != nil {
		return err
	}
	logger.Info("database ready")

	// The graph is loaded before the listener opens, so the process is
	// either able to route or not yet accepting connections — never
	// accepting requests it cannot answer.
	g, err := graph.Load(ctx, pool, logger)
	if err != nil {
		return err
	}

	api := httpapi.NewServer(cfg, logger, pool, g)

	// Load any hazards reported before this process started, so a restart
	// does not silently un-avoid known problems.
	if err := api.Hazards().Refresh(ctx, pool, g); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.Handler(),

		// Timeouts are not optional. Without them a single slow or
		// malicious client can hold a connection open forever, and enough
		// of those will exhaust the server.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second, // generous: route planning is CPU-bound
		IdleTimeout:       60 * time.Second,
	}

	// Serve in a background goroutine so this function can wait on the
	// shutdown signal below.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.Addr, "env", cfg.Env)
		// ListenAndServe always returns a non-nil error; ErrServerClosed
		// is the normal, expected one after Shutdown is called.
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Block until either the server fails or we are asked to stop.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Graceful shutdown: stop accepting new connections, then give
	// in-flight requests a bounded window to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}
