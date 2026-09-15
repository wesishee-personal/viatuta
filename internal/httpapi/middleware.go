package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// contextKey is a private type for context keys.
//
// Using a custom unexported type rather than a plain string guarantees no
// other package can accidentally collide with our keys, because no other
// package can construct a value of this type.
type contextKey string

const requestIDKey contextKey = "request_id"

// middleware wraps a handler with extra behavior.
//
// This is the standard Go pattern for cross-cutting concerns: a middleware
// takes a handler and returns a new handler that does something before
// and/or after calling the original.
type middleware func(http.Handler) http.Handler

// chain applies middlewares so that the FIRST listed is the OUTERMOST —
// i.e. chain(h, a, b) runs a, then b, then h. Without reversing the loop,
// the list would read backwards relative to execution order.
func chain(h http.Handler, ms ...middleware) http.Handler {
	for i := len(ms) - 1; i >= 0; i-- {
		h = ms[i](h)
	}
	return h
}

// RequestIDFrom returns the request ID stored in ctx, or "" if absent.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// withRequestID assigns each request a unique ID and echoes it back in the
// X-Request-ID header, so a user-reported failure can be traced to exact log
// lines.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			// rand.Read from crypto/rand never returns an error in
			// practice; if it somehow did, an empty ID is harmless.
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder captures the status code so the logging middleware can
// report it.
//
// http.ResponseWriter has no getter for the status that was written, so the
// only way to observe it is to wrap the writer and intercept WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader implicitly sends
	// 200, so record that here.
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += n
	return n, err
}

// withLogging records one structured line per request.
func withLogging(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

// withRecover turns a panic into a 500 instead of killing the process.
//
// In Go, a panic in a handler goroutine would normally crash the entire
// server. A nil map access in one route should not take down routing for
// everyone, so we recover, log, and fail just that request.
func withRecover(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"panic", rec,
						"path", r.URL.Path,
						"request_id", RequestIDFrom(r.Context()),
					)
					writeError(w, http.StatusInternalServerError, CodeInternal, "an internal error occurred")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// withCORS allows browser-based frontends to call the API.
//
// The frontend is out of scope for now, but without this a browser would
// block every cross-origin request, which is a confusing first thing to hit
// later. Origins are currently open; tighten this before production.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// A browser sends an OPTIONS "preflight" before the real request;
		// it expects a bare success with the headers above.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
