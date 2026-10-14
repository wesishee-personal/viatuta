package hazard

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunEveryCallsRepeatedlyAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan struct{})

	go func() {
		runEvery(ctx, 5*time.Millisecond, func(context.Context) error {
			calls.Add(1)
			return nil
		}, func(error) { t.Error("unexpected error") })
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d calls in 2s", calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runEvery did not stop after the context was cancelled")
	}
}

// A failed refresh must not end the loop: the next tick should try again.
func TestRunEveryKeepsGoingAfterAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls, errs atomic.Int32
	go runEvery(ctx, 5*time.Millisecond, func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("database blip")
		}
		return nil
	}, func(error) { errs.Add(1) })

	deadline := time.After(2 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("loop stopped after an error: %d calls", calls.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if errs.Load() != 1 {
		t.Errorf("reported %d errors, want 1", errs.Load())
	}
}

func TestRunEveryStaysQuietWhenShuttingDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var reported atomic.Bool

	done := make(chan struct{})
	go func() {
		runEvery(ctx, time.Millisecond, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err() // what a cancelled query returns
		}, func(error) { reported.Store(true) })
		close(done)
	}()

	<-started
	cancel()
	<-done
	if reported.Load() {
		t.Error("an error caused by shutdown was reported")
	}
}

func TestOverlayRefreshIntervalIsShortComparedWithLifetimes(t *testing.T) {
	// The shortest lifetime in the registry is an hour; the overlay must be
	// refreshed far more often than that or expiry would visibly lag.
	for _, k := range All() {
		if OverlayRefreshInterval*10 > k.TTL {
			t.Errorf("%s lives %v, only %d refresh intervals", k.Name, k.TTL, k.TTL/OverlayRefreshInterval)
		}
	}
}
