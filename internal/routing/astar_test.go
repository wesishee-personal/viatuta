package routing

import (
	"math"
	"math/rand"
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// geoGrid builds a grid graph with real coordinates, so the straight-line
// heuristic is meaningful. Spacing is about 100 m per cell.
func geoGrid(n int, seed int64) *graph.Graph {
	rng := rand.New(rand.NewSource(seed))
	idx := func(r, c int) int32 { return int32(r*n + c) }

	var edges []testEdge
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			// Lengths vary so the shortest path is not simply the fewest
			// hops, which would make the comparison trivial.
			if c+1 < n {
				l := float32(90 + rng.Intn(40))
				edges = append(edges, testEdge{idx(r, c), idx(r, c+1), l})
				edges = append(edges, testEdge{idx(r, c+1), idx(r, c), l})
			}
			if r+1 < n {
				l := float32(90 + rng.Intn(40))
				edges = append(edges, testEdge{idx(r, c), idx(r+1, c), l})
				edges = append(edges, testEdge{idx(r+1, c), idx(r, c), l})
			}
		}
	}

	g := buildGraph(n*n, edges)
	// Lay the grid out near Austin; 0.0009 degrees is roughly 100 m.
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			i := idx(r, c)
			g.NodeLat[i] = 30.25 + float64(r)*0.0009
			g.NodeLon[i] = -97.75 + float64(c)*0.0009
		}
	}
	return g
}

// TestAStarMatchesDijkstra is the central correctness check for Phase 7.
//
// A* is only optimal if its heuristic never overestimates the remaining cost.
// When that fails, the search does not crash or report an error — it quietly
// returns a worse route. The only way to catch that is to compare against the
// unguided search, which needs no heuristic to be correct.
func TestAStarMatchesDijkstra(t *testing.T) {
	g := geoGrid(12, 1)
	n := int32(g.NumNodes())

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 60; i++ {
		start := int32(rng.Intn(int(n)))
		goal := int32(rng.Intn(int(n)))
		if start == goal {
			continue
		}

		plain := lengthOpts()
		plain.NoHeuristic = true
		want, errW := Search(g, start, goal, plain)

		got, errG := Search(g, start, goal, lengthOpts())

		if (errW == nil) != (errG == nil) {
			t.Fatalf("%d -> %d: dijkstra err=%v but a* err=%v", start, goal, errW, errG)
		}
		if errW != nil {
			continue
		}
		if math.Abs(want.Cost-got.Cost) > 1e-6 {
			t.Errorf("%d -> %d: a* cost %.4f, dijkstra cost %.4f — the heuristic is not admissible",
				start, goal, got.Cost, want.Cost)
		}
	}
}

// TestAStarMatchesDijkstraWithSafetyCosts repeats the comparison with a cost
// function shaped like the real safety model: a per-edge multiplier of at
// least 1.0, plus turn costs.
func TestAStarMatchesDijkstraWithSafetyCosts(t *testing.T) {
	g := geoGrid(10, 3)
	rng := rand.New(rand.NewSource(11))

	// Assign each edge a multiplier in [1, 6], mimicking LTS penalties.
	mult := make([]float64, g.NumEdges())
	for i := range mult {
		mult[i] = 1 + rng.Float64()*5
	}

	opts := Options{
		EdgeCost: func(g *graph.Graph, e int32) float64 {
			return float64(g.LengthM[e]) * mult[e]
		},
		TurnCost: func(g *graph.Graph, in, out int32) float64 {
			if g.IsReverseOf(in, out) {
				return 300
			}
			return 8
		},
	}

	n := int32(g.NumNodes())
	for i := 0; i < 40; i++ {
		start := int32(rng.Intn(int(n)))
		goal := int32(rng.Intn(int(n)))
		if start == goal {
			continue
		}

		plain := opts
		plain.NoHeuristic = true
		want, err1 := Search(g, start, goal, plain)
		got, err2 := Search(g, start, goal, opts)

		if err1 != nil || err2 != nil {
			continue
		}
		if math.Abs(want.Cost-got.Cost) > 1e-6 {
			t.Errorf("%d -> %d: a* %.4f vs dijkstra %.4f", start, goal, got.Cost, want.Cost)
		}
	}
}

// TestAStarMatchesDijkstraWithBudget checks the heuristic stays correct in
// the two-dimensional state space the stress budget introduces.
func TestAStarMatchesDijkstraWithBudget(t *testing.T) {
	g := geoGrid(9, 5)
	rng := rand.New(rand.NewSource(13))

	over := make([]bool, g.NumEdges())
	for i := range over {
		over[i] = rng.Float64() < 0.3
	}

	opts := Options{
		EdgeCost: func(g *graph.Graph, e int32) float64 {
			c := float64(g.LengthM[e])
			if over[e] {
				c *= 12
			}
			return c
		},
		Over:    func(g *graph.Graph, e int32) bool { return over[e] },
		BudgetM: 400,
	}

	n := int32(g.NumNodes())
	for i := 0; i < 40; i++ {
		start := int32(rng.Intn(int(n)))
		goal := int32(rng.Intn(int(n)))
		if start == goal {
			continue
		}

		plain := opts
		plain.NoHeuristic = true
		want, err1 := Search(g, start, goal, plain)
		got, err2 := Search(g, start, goal, opts)

		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("%d -> %d: feasibility disagrees (dijkstra %v, a* %v)", start, goal, err1, err2)
		}
		if err1 != nil {
			continue
		}
		if math.Abs(want.Cost-got.Cost) > 1e-6 {
			t.Errorf("%d -> %d: a* %.4f vs dijkstra %.4f", start, goal, got.Cost, want.Cost)
		}
		if got.BudgetUsedM > opts.BudgetM+1e-6 {
			t.Errorf("a* exceeded the budget: %v > %v", got.BudgetUsedM, opts.BudgetM)
		}
	}
}

// TestAStarExploresLess confirms the heuristic actually earns its keep.
func TestAStarExploresLess(t *testing.T) {
	g := geoGrid(20, 2)
	start, goal := int32(0), int32(g.NumNodes()-1)

	plain := lengthOpts()
	plain.NoHeuristic = true
	d, err := Search(g, start, goal, plain)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Search(g, start, goal, lengthOpts())
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("dijkstra explored %d states, a* explored %d (%.1fx fewer)",
		d.Expansions, a.Expansions, float64(d.Expansions)/float64(a.Expansions))

	if a.Expansions >= d.Expansions {
		t.Errorf("a* explored %d states, no better than dijkstra's %d", a.Expansions, d.Expansions)
	}
}

// TestWorkspaceReuseIsClean checks that pooled buffers do not leak state
// between searches. A stale distance left behind would make a later search
// skip edges it should have explored.
func TestWorkspaceReuseIsClean(t *testing.T) {
	g := geoGrid(8, 9)

	first, err := Search(g, 0, int32(g.NumNodes()-1), lengthOpts())
	if err != nil {
		t.Fatal(err)
	}

	// Run several unrelated searches through the same pool, then repeat the
	// original and require an identical answer.
	for i := 0; i < 20; i++ {
		_, _ = Search(g, int32(i%g.NumNodes()), int32((i*7)%g.NumNodes()), lengthOpts())
	}

	again, err := Search(g, 0, int32(g.NumNodes()-1), lengthOpts())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(first.Cost-again.Cost) > 1e-9 || len(first.Edges) != len(again.Edges) {
		t.Errorf("pooled workspace leaked state: first cost %.4f/%d edges, repeat %.4f/%d edges",
			first.Cost, len(first.Edges), again.Cost, len(again.Edges))
	}
}
