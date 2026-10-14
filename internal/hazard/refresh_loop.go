package hazard

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/graph"
)

// OverlayRefreshInterval is how often the routing overlay is rebuilt even when
// nobody reports anything.
//
// The overlay is built from the hazards that are active at that moment, so
// without this it is only correct right after a report or confirmation. Once
// lifetimes are measured in hours that matters: an accident that expired at
// 3pm would keep steering routes until the next unrelated report arrived. A
// rebuild also moves every hazard along its fade toward expiry. A minute keeps
// the overlay within a minute of the truth, and a rebuild takes a fraction of
// a second.
const OverlayRefreshInterval = time.Minute

// RefreshEvery rebuilds the overlay every interval until ctx is cancelled.
// Failures are logged and the previous overlay stays in force, so a database
// blip never leaves the router without one.
func (i *Index) RefreshEvery(ctx context.Context, pool *pgxpool.Pool, g *graph.Graph,
	interval time.Duration, logger *slog.Logger) {

	runEvery(ctx, interval,
		func(ctx context.Context) error {
			start := time.Now()
			if err := i.Refresh(ctx, pool, g); err != nil {
				return err
			}
			logger.Debug("hazard overlay refreshed", "duration", time.Since(start).Round(time.Millisecond))
			return nil
		},
		func(err error) { logger.Error("periodic hazard overlay refresh failed", "error", err) })
}

// runEvery calls fn once per interval until ctx is done, reporting failures to
// onErr. It runs fn on the calling goroutine, so runs never overlap: a slow
// refresh delays the next one rather than piling up behind it.
func runEvery(ctx context.Context, interval time.Duration, fn func(context.Context) error, onErr func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// An error caused by shutting down is not worth reporting.
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				onErr(err)
			}
		}
	}
}
