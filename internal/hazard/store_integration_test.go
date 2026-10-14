package hazard

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
)

// These tests need a migrated Postgres/PostGIS database. They are skipped
// unless VIATUTA_TEST_DATABASE_URL is set, e.g.
//
//	VIATUTA_TEST_DATABASE_URL='postgres://viatuta:viatuta@localhost:5433/viatuta?sslmode=disable' \
//	  go test ./internal/hazard/
//
// They only touch rows they create: hazards sit in the Gulf of Guinea, far
// from any real report, and accounts and hazards are deleted afterwards.

const (
	testLat = 1.0
	testLon = 1.0
	// One metre of latitude, in degrees.
	metreDeg = 1.0 / 111_320.0
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("VIATUTA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("VIATUTA_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newUser inserts a throwaway account and removes it, with everything it
// reported, when the test ends.
func newUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	email := fmt.Sprintf("hazard-it-%d@example.test", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO user_account (email, password_hash) VALUES ($1, 'x') RETURNING id::text`, email).
		Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM hazard_report WHERE user_id = $1`, id) //nolint:errcheck
		pool.Exec(ctx, `DELETE FROM user_account WHERE id = $1`, id)       //nolint:errcheck
	})
	return id
}

// at returns a point `north` metres north of the test origin.
func at(north float64) geo.LatLon {
	return geo.LatLon{Lat: testLat + north*metreDeg, Lon: testLon}
}

func report(t *testing.T, pool *pgxpool.Pool, user, kind string, p geo.LatLon) (Report, bool) {
	t.Helper()
	rep, created, err := Create(context.Background(), pool, Submission{Location: p, Kind: kind}, user)
	if err != nil {
		t.Fatalf("Create(%s): %v", kind, err)
	}
	return rep, created
}

func TestCreateSetsLifetimeFromKind(t *testing.T) {
	pool := testPool(t)
	u := newUser(t, pool)

	rep, created := report(t, pool, u, "accident", at(0))
	if !created {
		t.Fatal("first report should create a hazard")
	}
	if got := rep.ExpiresAt.Sub(rep.CreatedAt); got != 3*time.Hour {
		t.Errorf("accident life = %v, want 3h", got)
	}
}

func TestNearbyReportOfSameKindMerges(t *testing.T) {
	pool := testPool(t)
	a, b := newUser(t, pool), newUser(t, pool)

	first, _ := report(t, pool, a, "pothole", at(0))
	second, created := report(t, pool, b, "pothole", at(20)) // within the 25 m place radius

	if created {
		t.Fatal("a pothole 20 m from another should merge, not create")
	}
	if second.ID != first.ID {
		t.Errorf("merged into %d, want %d", second.ID, first.ID)
	}
	if second.Confirmations != 1 {
		t.Errorf("confirmations = %d, want 1", second.Confirmations)
	}
}

func TestMergeRadiusDiffersByKind(t *testing.T) {
	pool := testPool(t)
	a, b := newUser(t, pool), newUser(t, pool)

	// 40 m apart: inside the 50 m area radius, outside the 25 m place radius.
	report(t, pool, a, "accident", at(1000))
	if _, created := report(t, pool, b, "accident", at(1040)); created {
		t.Error("accidents 40 m apart should merge (area radius 50 m)")
	}

	report(t, pool, a, "pothole", at(2000))
	if _, created := report(t, pool, b, "pothole", at(2040)); !created {
		t.Error("potholes 40 m apart should stay separate (place radius 25 m)")
	}
}

func TestMergeBoundaryIsMeasuredInMetresEastWest(t *testing.T) {
	// Degrees of longitude are shorter than degrees of latitude away from the
	// equator. A bounding-box pre-filter sized in latitude degrees would miss
	// a duplicate that is due east: at 30° latitude, 49 m of longitude is
	// 0.00051°, just outside a naive 0.0005° box. Use a real latitude to check.
	pool := testPool(t)
	a, b := newUser(t, pool), newUser(t, pool)

	lat := 30.0
	lonDeg := func(metres float64) float64 { return metres / (111_320 * math.Cos(lat*math.Pi/180)) }
	base := geo.LatLon{Lat: lat + 0.37, Lon: 1.0} // an unpopulated longitude

	Create(context.Background(), pool, Submission{Location: base, Kind: "accident"}, a) //nolint:errcheck
	east := geo.LatLon{Lat: base.Lat, Lon: base.Lon + lonDeg(49)}
	_, created, err := Create(context.Background(), pool, Submission{Location: east, Kind: "accident"}, b)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("an accident 49 m due east should merge into the first")
	}
}

func TestDifferentKindsDoNotMerge(t *testing.T) {
	pool := testPool(t)
	a, b := newUser(t, pool), newUser(t, pool)

	report(t, pool, a, "glass", at(3000))
	if _, created := report(t, pool, b, "pothole", at(3001)); !created {
		t.Error("a pothole next to glass is a different hazard")
	}
}

func TestReporterCannotConfirmOwnOrVoteTwice(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	a, b := newUser(t, pool), newUser(t, pool)

	rep, _ := report(t, pool, a, "debris", at(4000))

	if _, err := Confirm(ctx, pool, rep.ID, a); !errors.Is(err, ErrOwnReport) {
		t.Errorf("confirming your own report: err = %v, want ErrOwnReport", err)
	}
	if _, err := Confirm(ctx, pool, rep.ID, b); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	if _, err := Confirm(ctx, pool, rep.ID, b); !errors.Is(err, ErrAlreadyVoted) {
		t.Errorf("confirming twice: err = %v, want ErrAlreadyVoted", err)
	}
}

func TestReReportingYourOwnHazardChangesNothing(t *testing.T) {
	pool := testPool(t)
	a := newUser(t, pool)

	first, _ := report(t, pool, a, "debris", at(5000))
	again, created := report(t, pool, a, "debris", at(5002))

	if created || again.ID != first.ID {
		t.Fatalf("re-report should return the same hazard (created=%v id=%d want %d)", created, again.ID, first.ID)
	}
	if again.Confirmations != 0 {
		t.Errorf("confirmations = %d; reporting your own hazard twice must not inflate it", again.Confirmations)
	}
}

func TestConfirmationsExtendLifeUpToMax(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	reporter := newUser(t, pool)
	rep, _ := report(t, pool, reporter, "accident", at(6000)) // 3 h, +1 h each, max 8 h

	var last Report
	for i := 0; i < 10; i++ {
		var err error
		last, err = Confirm(ctx, pool, rep.ID, newUser(t, pool))
		if err != nil {
			t.Fatalf("confirmation %d: %v", i+1, err)
		}
		if i == 2 { // 3h + 3×1h
			if got := last.ExpiresAt.Sub(last.CreatedAt); got != 6*time.Hour {
				t.Errorf("after 3 confirmations life = %v, want 6h", got)
			}
		}
	}
	if got := last.ExpiresAt.Sub(last.CreatedAt); got != 8*time.Hour {
		t.Errorf("after 10 confirmations life = %v, want the 8h cap", got)
	}
}

func TestReporterCanResolveAlone(t *testing.T) {
	pool := testPool(t)
	a := newUser(t, pool)
	rep, _ := report(t, pool, a, "glass", at(7000))

	got, err := Resolve(context.Background(), pool, rep.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt.After(time.Now()) {
		t.Errorf("reporter's resolve should take the hazard down; expires %v", got.ExpiresAt)
	}
}

func TestOtherRidersNeedTwoVotesToResolve(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	reporter, b, c := newUser(t, pool), newUser(t, pool), newUser(t, pool)
	rep, _ := report(t, pool, reporter, "glass", at(8000))

	one, err := Resolve(ctx, pool, rep.ID, b)
	if err != nil {
		t.Fatal(err)
	}
	if !one.ExpiresAt.After(time.Now()) {
		t.Fatal("one rider alone must not take a hazard down")
	}
	if _, err := Resolve(ctx, pool, rep.ID, b); !errors.Is(err, ErrAlreadyVoted) {
		t.Errorf("resolving twice: err = %v, want ErrAlreadyVoted", err)
	}

	two, err := Resolve(ctx, pool, rep.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if two.ExpiresAt.After(time.Now()) {
		t.Error("two riders should take it down")
	}

	// And a resolved hazard is gone for everyone.
	if _, err := Confirm(ctx, pool, rep.ID, newUser(t, pool)); !errors.Is(err, ErrNotFound) {
		t.Errorf("confirming a resolved hazard: err = %v, want ErrNotFound", err)
	}
	var resolvedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT resolved_at FROM hazard_report WHERE id = $1`, rep.ID).Scan(&resolvedAt); err != nil || resolvedAt == nil {
		t.Errorf("resolved_at not recorded (err=%v)", err)
	}
}

func TestResolvedHazardDoesNotAbsorbNewReports(t *testing.T) {
	pool := testPool(t)
	a, b := newUser(t, pool), newUser(t, pool)

	first, _ := report(t, pool, a, "fallen_tree", at(9000))
	if _, err := Resolve(context.Background(), pool, first.ID, a); err != nil {
		t.Fatal(err)
	}
	again, created := report(t, pool, b, "fallen_tree", at(9000))
	if !created || again.ID == first.ID {
		t.Error("a new report at a cleared spot should create a fresh hazard")
	}
}

func TestListOmitsResolved(t *testing.T) {
	pool := testPool(t)
	a := newUser(t, pool)
	rep, _ := report(t, pool, a, "loose_animal", at(10000))

	find := func() bool {
		list, err := ListInBBox(context.Background(), pool, testLon-0.01, testLat-0.01, testLon+0.01, testLat+0.2, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range list {
			if h.ID == rep.ID {
				return true
			}
		}
		return false
	}
	if !find() {
		t.Fatal("an active hazard should be listed")
	}
	if _, err := Resolve(context.Background(), pool, rep.ID, a); err != nil {
		t.Fatal(err)
	}
	if find() {
		t.Error("a resolved hazard should not be listed")
	}
}
