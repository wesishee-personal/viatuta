package hazard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
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

func reportHeading(t *testing.T, pool *pgxpool.Pool, user, kind string, p geo.LatLon, heading *float64) (Report, bool) {
	t.Helper()
	rep, created, err := Create(context.Background(), pool,
		Submission{Location: p, Kind: kind, HeadingDeg: heading}, user)
	if err != nil {
		t.Fatalf("Create(%s): %v", kind, err)
	}
	return rep, created
}

func TestHeadingIsStoredAndReturned(t *testing.T) {
	pool := testPool(t)
	u := newUser(t, pool)

	rep, _ := reportHeading(t, pool, u, "pothole", at(11000), f(359.6))
	if rep.HeadingDeg == nil || *rep.HeadingDeg != 0 {
		t.Errorf("heading = %v, want 0 (359.6 rounds through north)", rep.HeadingDeg)
	}
	none, _ := reportHeading(t, pool, u, "pothole", at(11500), nil)
	if none.HeadingDeg != nil {
		t.Errorf("a report with no heading came back with %d", *none.HeadingDeg)
	}
}

func TestDirectionalReportsMergeOnlyWhenFacingTheSameWay(t *testing.T) {
	pool := testPool(t)
	a, b, c, d := newUser(t, pool), newUser(t, pool), newUser(t, pool), newUser(t, pool)

	first, _ := reportHeading(t, pool, a, "pothole", at(12000), f(90)) // eastbound

	if _, created := reportHeading(t, pool, b, "pothole", at(12005), f(270)); !created {
		t.Error("a pothole seen heading west is not the eastbound one")
	}
	if rep, created := reportHeading(t, pool, c, "pothole", at(12005), f(100)); created || rep.ID != first.ID {
		t.Errorf("heading 100 vs 90 should merge (created=%v id=%d want %d)", created, rep.ID, first.ID)
	}
	// A bidirectional report is compatible with a directional one, so it merges
	// into whichever is nearest rather than adding a third marker.
	if _, created := reportHeading(t, pool, d, "pothole", at(12005), nil); created {
		t.Error("a bidirectional report next to existing potholes should merge, not create")
	}
}

func TestNonDirectionalKindsIgnoreHeadingWhenMerging(t *testing.T) {
	pool := testPool(t)
	a, b := newUser(t, pool), newUser(t, pool)

	first, _ := reportHeading(t, pool, a, "road_closed", at(13000), f(90))
	rep, created := reportHeading(t, pool, b, "road_closed", at(13005), f(270))
	if created || rep.ID != first.ID {
		t.Error("a closure is a closure whichever way you were facing")
	}
}

// TestOverlayAppliesDirectionalHazardToOneDirectionOnly runs the real overlay
// query against the real graph: a directional hazard must score the edge it
// was reported on and leave the opposite carriageway alone.
func TestOverlayAppliesDirectionalHazardToOneDirectionOnly(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	g, err := graph.Load(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skipf("no routing graph in this database: %v", err)
	}

	e, r, ok := straightTwoWayEdge(g)
	if !ok {
		t.Skip("no suitable two-way edge in the graph")
	}
	var lat, lon float64
	if err := pool.QueryRow(ctx, `
		SELECT ST_Y(ST_LineInterpolatePoint(geom, 0.5)), ST_X(ST_LineInterpolatePoint(geom, 0.5))
		FROM routing_edge WHERE id = $1`, g.DBID[e]).Scan(&lat, &lon); err != nil {
		t.Fatal(err)
	}
	where := geo.LatLon{Lat: lat, Lon: lon}
	heading := float64(g.BearingStart[e])

	score := func(kind string, h *float64) (onE, onReverse float32) {
		u := newUser(t, pool)
		rep, created := reportHeading(t, pool, u, kind, where, h)
		if !created {
			t.Fatalf("test hazard merged into an existing one (id %d)", rep.ID)
		}
		defer pool.Exec(ctx, `DELETE FROM hazard_report WHERE id = $1`, rep.ID) //nolint:errcheck

		idx := NewIndex(g.NumEdges())
		if err := idx.Refresh(ctx, pool, g); err != nil {
			t.Fatal(err)
		}
		s := idx.Snapshot().Scores
		return s[e], s[r]
	}

	if onE, onR := score("pothole", &heading); onE <= 0 || onR != 0 {
		t.Errorf("directional pothole: score on its own edge = %v (want > 0), on the reverse = %v (want 0)", onE, onR)
	}
	if onE, onR := score("pothole", nil); onE <= 0 || onR <= 0 {
		t.Errorf("a bidirectional pothole should score both directions, got %v and %v", onE, onR)
	}
	if onE, onR := score("construction", &heading); onE <= 0 || onR <= 0 {
		t.Errorf("a non-directional kind should ignore heading, got %v and %v", onE, onR)
	}
}

// straightTwoWayEdge finds a long, nearly straight edge that has a reverse
// edge, so "same direction" and "opposite direction" are unambiguous.
func straightTwoWayEdge(g *graph.Graph) (e, reverse int32, ok bool) {
	for i := int32(0); int(i) < g.NumEdges(); i++ {
		if g.LengthM[i] < 60 || g.BearingStart[i] == 0 && g.BearingEnd[i] == 0 {
			continue
		}
		if AngleDiff(float64(g.BearingStart[i]), float64(g.BearingEnd[i])) > 15 {
			continue
		}
		start, end := g.OutEdges(g.To[i])
		for j := start; j < end; j++ {
			if g.IsReverseOf(i, j) {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// TestOverlayClosureScalesWithReportersAndSkipsOrdinaryScore runs the closure
// query on the real graph: the penalty must follow the number of riders, apply
// in both directions, and not also appear as an ordinary hazard score.
func TestOverlayClosureScalesWithReportersAndSkipsOrdinaryScore(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	g, err := graph.Load(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skipf("no routing graph in this database: %v", err)
	}
	e, r, ok := straightTwoWayEdge(g)
	if !ok {
		t.Skip("no suitable two-way edge in the graph")
	}
	var lat, lon float64
	if err := pool.QueryRow(ctx, `
		SELECT ST_Y(ST_LineInterpolatePoint(geom, 0.5)), ST_X(ST_LineInterpolatePoint(geom, 0.5))
		FROM routing_edge WHERE id = $1`, g.DBID[e]).Scan(&lat, &lon); err != nil {
		t.Fatal(err)
	}
	where := geo.LatLon{Lat: lat, Lon: lon}

	reporter := newUser(t, pool)
	heading := float64(g.BearingStart[e]) // a heading must be ignored for closures
	rep, created := reportHeading(t, pool, reporter, "road_closed", where, &heading)
	if !created {
		t.Fatalf("test hazard merged into an existing one (id %d)", rep.ID)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM hazard_report WHERE id = $1`, rep.ID) }) //nolint:errcheck

	refresh := func() Snapshot {
		idx := NewIndex(g.NumEdges())
		if err := idx.Refresh(ctx, pool, g); err != nil {
			t.Fatal(err)
		}
		return idx.Snapshot()
	}

	snap := refresh()
	want1 := float32(ClosurePenalty(1))
	if snap.Closures[e] != want1 || snap.Closures[r] != want1 {
		t.Errorf("one report: closure on edge/reverse = %v/%v, want %v on both", snap.Closures[e], snap.Closures[r], want1)
	}
	if snap.Scores[e] != 0 {
		t.Errorf("a closure also produced an ordinary hazard score of %v", snap.Scores[e])
	}

	for i := 0; i < 2; i++ { // two more riders confirm: three in all
		if _, err := Confirm(ctx, pool, rep.ID, newUser(t, pool)); err != nil {
			t.Fatal(err)
		}
	}
	snap = refresh()
	if want3 := float32(ClosurePenalty(3)); snap.Closures[e] != want3 {
		t.Errorf("three reporters: closure = %v, want %v", snap.Closures[e], want3)
	}
	if snap.Closures[e] <= want1 {
		t.Error("more reporters did not make the closure heavier")
	}

	if _, err := Resolve(ctx, pool, rep.ID, reporter); err != nil {
		t.Fatal(err)
	}
	if got := refresh().Closures[e]; got != 0 {
		t.Errorf("a resolved closure still penalises the road: %v", got)
	}
}

// TestExpiredHazardKeepsSteeringUntilTheNextRefresh documents WHY the overlay
// is refreshed periodically: it is a snapshot of the hazards active when it
// was built, so a hazard that expires afterwards stays in it until something
// rebuilds it.
func TestExpiredHazardKeepsSteeringUntilTheNextRefresh(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	g, err := graph.Load(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Skipf("no routing graph in this database: %v", err)
	}
	e, _, ok := straightTwoWayEdge(g)
	if !ok {
		t.Skip("no suitable two-way edge in the graph")
	}
	var lat, lon float64
	if err := pool.QueryRow(ctx, `
		SELECT ST_Y(ST_LineInterpolatePoint(geom, 0.5)), ST_X(ST_LineInterpolatePoint(geom, 0.5))
		FROM routing_edge WHERE id = $1`, g.DBID[e]).Scan(&lat, &lon); err != nil {
		t.Fatal(err)
	}

	u := newUser(t, pool)
	rep, created := report(t, pool, u, "accident", geo.LatLon{Lat: lat, Lon: lon})
	if !created {
		t.Fatalf("test hazard merged into an existing one (id %d)", rep.ID)
	}

	idx := NewIndex(g.NumEdges())
	start := time.Now()
	if err := idx.Refresh(ctx, pool, g); err != nil {
		t.Fatal(err)
	}
	t.Logf("overlay refresh over %d edges took %v", g.NumEdges(), time.Since(start).Round(time.Millisecond))
	if idx.Snapshot().Scores[e] <= 0 {
		t.Fatal("the active hazard should score its edge")
	}

	// The hazard expires...
	if _, err := pool.Exec(ctx,
		`UPDATE hazard_report SET expires_at = now() - interval '1 minute' WHERE id = $1`, rep.ID); err != nil {
		t.Fatal(err)
	}
	// ...but the overlay does not know until it is rebuilt.
	if idx.Snapshot().Scores[e] <= 0 {
		t.Fatal("expected the stale overlay to still carry the expired hazard")
	}
	if err := idx.Refresh(ctx, pool, g); err != nil {
		t.Fatal(err)
	}
	if got := idx.Snapshot().Scores[e]; got != 0 {
		t.Errorf("after a refresh the expired hazard still scores %v", got)
	}
}
