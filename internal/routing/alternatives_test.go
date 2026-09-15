package routing

import (
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// parallelRoutes builds three disjoint corridors between node 0 and node 1,
// of clearly different lengths.
func parallelRoutes() *graph.Graph {
	// Three corridors at 200 m, 230 m and 260 m. They are deliberately kept
	// within MaxCostRatio of each other: a corridor more than 1.6x the best
	// is rejected by design, which TestAlternativesRejectsMuchWorseRoutes
	// covers separately.
	edges := []testEdge{
		{0, 2, 100}, {2, 1, 100},
		{0, 3, 115}, {3, 1, 115},
		{0, 4, 130}, {4, 1, 130},
	}
	g := buildGraph(5, edges)
	for i := range g.NodeLat {
		g.NodeLat[i] = 30.25
		g.NodeLon[i] = -97.75
	}
	return g
}

func TestAlternativesFindsDistinctRoutes(t *testing.T) {
	g := parallelRoutes()

	routes, err := Alternatives(g, 0, 1, lengthOpts(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3 (primary plus two alternatives)", len(routes))
	}

	// The primary must be the genuinely shortest.
	if routes[0].LengthM != 200 {
		t.Errorf("primary length = %v, want 200", routes[0].LengthM)
	}
	// Each must use a different corridor, so no edge may repeat.
	seen := map[int32]bool{}
	for i, r := range routes {
		for _, e := range r.Edges {
			if seen[e] {
				t.Errorf("route %d reuses edge %d; the routes are not distinct", i, e)
			}
			seen[e] = true
		}
	}
}

// TestAlternativeCostsAreTrueCosts checks that the penalty used to push the
// search elsewhere does not leak into the reported cost. Otherwise every
// alternative would look twice as bad as it is.
func TestAlternativeCostsAreTrueCosts(t *testing.T) {
	g := parallelRoutes()

	routes, err := Alternatives(g, 0, 1, lengthOpts(), 2)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range routes {
		if r.Cost != r.LengthM {
			t.Errorf("route %d: cost %v should equal length %v under a length-only model (penalty leaked)",
				i, r.Cost, r.LengthM)
		}
	}
}

func TestAlternativesAreOrderedByQuality(t *testing.T) {
	g := parallelRoutes()
	routes, err := Alternatives(g, 0, 1, lengthOpts(), 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(routes); i++ {
		if routes[i].Cost < routes[i-1].Cost {
			t.Errorf("route %d (cost %v) is better than route %d (cost %v); "+
				"the primary must be the best", i, routes[i].Cost, i-1, routes[i-1].Cost)
		}
	}
}

// TestAlternativesRejectsMuchWorseRoutes checks the quality cap. Offering a
// rider something far worse is noise, not a choice.
func TestAlternativesRejectsMuchWorseRoutes(t *testing.T) {
	// One short corridor and one absurdly long one.
	edges := []testEdge{
		{0, 2, 100}, {2, 1, 100},
		{0, 3, 5000}, {3, 1, 5000},
	}
	g := buildGraph(4, edges)
	for i := range g.NodeLat {
		g.NodeLat[i] = 30.25
		g.NodeLon[i] = -97.75
	}

	routes, err := Alternatives(g, 0, 1, lengthOpts(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Errorf("got %d routes; a 50x detour should be rejected", len(routes))
	}
}

// TestAlternativesWhenNoneExist checks a corridor with a single option.
func TestAlternativesWhenNoneExist(t *testing.T) {
	edges := []testEdge{{0, 1, 100}, {1, 2, 100}}
	g := buildGraph(3, edges)
	for i := range g.NodeLat {
		g.NodeLat[i] = 30.25
		g.NodeLon[i] = -97.75
	}

	routes, err := Alternatives(g, 0, 2, lengthOpts(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Errorf("got %d routes, want 1 when no alternative exists", len(routes))
	}
}

func TestAlternativesZeroRequested(t *testing.T) {
	g := parallelRoutes()
	routes, err := Alternatives(g, 0, 1, lengthOpts(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Errorf("got %d routes, want just the primary", len(routes))
	}
}

// TestAlternativesPropagatesUnroutable checks that an impossible request
// fails rather than returning an empty list.
func TestAlternativesPropagatesUnroutable(t *testing.T) {
	edges := []testEdge{{0, 1, 100}, {2, 3, 100}}
	g := buildGraph(4, edges)
	for i := range g.NodeLat {
		g.NodeLat[i] = 30.25
		g.NodeLon[i] = -97.75
	}

	if _, err := Alternatives(g, 0, 3, lengthOpts(), 2); err == nil {
		t.Error("expected an error routing between disconnected components")
	}
}
