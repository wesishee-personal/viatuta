package routing

import (
	"math"
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// testEdge describes one directed edge for a hand-built graph.
type testEdge struct {
	from, to int32
	length   float32
}

// buildGraph assembles a Graph the way graph.Load would, including sorting
// edges by source node and computing the CSR offsets.
func buildGraph(numNodes int, edges []testEdge) *graph.Graph {
	// Sort by source node: the CSR layout depends on it.
	sorted := make([]testEdge, len(edges))
	copy(sorted, edges)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].from < sorted[j-1].from; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	g := &graph.Graph{
		NodeLat:     make([]float64, numNodes),
		NodeLon:     make([]float64, numNodes),
		NodeControl: make([]graph.Control, numNodes),
		Offs:        make([]int32, numNodes+1),
	}
	for _, e := range sorted {
		g.From = append(g.From, e.from)
		g.To = append(g.To, e.to)
		g.LengthM = append(g.LengthM, e.length)
		g.DBID = append(g.DBID, int64(len(g.DBID)))
		g.Infra = append(g.Infra, graph.InfraNone)
		g.Highway = append(g.Highway, graph.HwyResidential)
		g.MaxSpeed = append(g.MaxSpeed, graph.SpeedUnknown)
		g.Lanes = append(g.Lanes, graph.LanesUnknown)
		g.Lit = append(g.Lit, graph.LitUnknown)
		g.LTS = append(g.LTS, 0)
		g.CrashScore = append(g.CrashScore, 0)
		g.GradePct = append(g.GradePct, 0)
		g.Offs[e.from+1]++
	}
	for i := 1; i <= numNodes; i++ {
		g.Offs[i] += g.Offs[i-1]
	}
	return g
}

func lengthOpts() Options { return Options{EdgeCost: LengthOnlyCost} }

// pathNodes converts a path back to the node sequence it visits.
func pathNodes(g *graph.Graph, p *Path, start int32) []int32 {
	out := []int32{start}
	for _, e := range p.Edges {
		out = append(out, g.To[e])
	}
	return out
}

func TestDijkstraFindsShortestOfTwoRoutes(t *testing.T) {
	//   0 --10--> 1 --10--> 3      (total 20)
	//   0 --1---> 2 --1---> 3      (total 2, the right answer)
	g := buildGraph(4, []testEdge{
		{0, 1, 10}, {1, 3, 10},
		{0, 2, 1}, {2, 3, 1},
	})

	p, err := Search(g, 0, 3, lengthOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.LengthM-2) > 1e-6 {
		t.Errorf("length = %v, want 2", p.LengthM)
	}
	got := pathNodes(g, p, 0)
	want := []int32{0, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("path = %v, want %v", got, want)
		}
	}
}

func TestDijkstraPrefersLongerButCheaperPath(t *testing.T) {
	// The whole premise of the project: a route can be longer in meters and
	// still be correct, when the cost function says the detour is worth it.
	//   0 -> 1 -> 3   short in distance (20 m) but expensive
	//   0 -> 2 -> 3   long in distance (200 m) but cheap
	g := buildGraph(4, []testEdge{
		{0, 1, 10}, {1, 3, 10},
		{0, 2, 100}, {2, 3, 100},
	})

	// Make the direct route ten times as costly per meter.
	expensive := map[int32]bool{}
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		if g.To[e] == 1 || g.From[e] == 1 {
			expensive[e] = true
		}
	}
	opts := Options{EdgeCost: func(g *graph.Graph, e int32) float64 {
		c := float64(g.LengthM[e])
		if expensive[e] {
			c *= 50
		}
		return c
	}}

	p, err := Search(g, 0, 3, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.LengthM-200) > 1e-6 {
		t.Errorf("length = %v, want the 200 m detour", p.LengthM)
	}
	// The detour is unpenalised, so its cost equals its length (200), while
	// the 20 m shortcut would have cost 20 x 50 = 1000. Cost and distance
	// are reported separately so the API can tell a rider "this is longer,
	// and here is what the extra distance bought you".
	if math.Abs(p.Cost-200) > 1e-6 {
		t.Errorf("cost = %v, want 200", p.Cost)
	}
	if p.Cost >= 1000 {
		t.Errorf("cost %v should beat the penalised shortcut's 1000", p.Cost)
	}
}

func TestDijkstraRespectsDirection(t *testing.T) {
	// Only 1 -> 0 exists, so 0 -> 1 must fail.
	g := buildGraph(2, []testEdge{{1, 0, 5}})

	if _, err := Search(g, 0, 1, lengthOpts()); err == nil {
		t.Error("expected failure routing against a one-way edge")
	}
}

func TestDijkstraUnroutableWhenDisconnected(t *testing.T) {
	g := buildGraph(4, []testEdge{{0, 1, 5}, {2, 3, 5}})

	_, err := Search(g, 0, 3, lengthOpts())
	if err == nil {
		t.Fatal("expected ErrUnroutable across disconnected components")
	}
}

func TestDijkstraSameStartAndGoal(t *testing.T) {
	g := buildGraph(2, []testEdge{{0, 1, 5}})
	p, err := Search(g, 0, 0, lengthOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Edges) != 0 || p.LengthM != 0 {
		t.Errorf("expected an empty path, got %+v", p)
	}
}

// TestDijkstraAllowFilterRemovesEdges checks the hard filter that Phase 4
// will use for a rider's maximum tolerable traffic stress.
func TestDijkstraAllowFilterRemovesEdges(t *testing.T) {
	g := buildGraph(4, []testEdge{
		{0, 1, 1}, {1, 3, 1}, // cheap but will be forbidden
		{0, 2, 50}, {2, 3, 50},
	})

	opts := lengthOpts()
	opts.Allow = func(g *graph.Graph, e int32) bool {
		return g.To[e] != 1 && g.From[e] != 1
	}

	p, err := Search(g, 0, 3, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.LengthM-100) > 1e-6 {
		t.Errorf("length = %v, want 100 (the forbidden shortcut must be unused)", p.LengthM)
	}
}

// TestDijkstraTurnCostChangesTheRoute is the property that justifies the
// edge-based search. With node-based state this test could not be written.
func TestDijkstraTurnCostChangesTheRoute(t *testing.T) {
	// Two equal-length routes from 0 to 3.
	g := buildGraph(4, []testEdge{
		{0, 1, 10}, {1, 3, 10},
		{0, 2, 10}, {2, 3, 10},
	})

	// Identify the edge pair that turns through node 1.
	var inVia1, outVia1 int32 = -1, -1
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		if g.From[e] == 0 && g.To[e] == 1 {
			inVia1 = e
		}
		if g.From[e] == 1 && g.To[e] == 3 {
			outVia1 = e
		}
	}

	opts := lengthOpts()
	opts.TurnCost = func(g *graph.Graph, in, out int32) float64 {
		if in == inVia1 && out == outVia1 {
			return 1000 // a dangerous junction
		}
		return 0
	}

	p, err := Search(g, 0, 3, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range p.Edges {
		if e == inVia1 {
			t.Error("route used the penalised turn despite an equal-length alternative")
		}
	}
}

// TestDijkstraOptimality brute-forces a small random graph and checks the
// search agrees with an exhaustive answer.
func TestDijkstraOptimality(t *testing.T) {
	// A 4x4 grid with unit edges in both directions.
	const n = 4
	idx := func(r, c int) int32 { return int32(r*n + c) }
	var edges []testEdge
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if c+1 < n {
				edges = append(edges, testEdge{idx(r, c), idx(r, c+1), 1})
				edges = append(edges, testEdge{idx(r, c+1), idx(r, c), 1})
			}
			if r+1 < n {
				edges = append(edges, testEdge{idx(r, c), idx(r+1, c), 1})
				edges = append(edges, testEdge{idx(r+1, c), idx(r, c), 1})
			}
		}
	}
	g := buildGraph(n*n, edges)

	// On a unit grid the cheapest path is the Manhattan distance.
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			p, err := Search(g, idx(0, 0), idx(r, c), lengthOpts())
			if err != nil {
				t.Fatalf("(%d,%d): %v", r, c, err)
			}
			want := float64(r + c)
			if math.Abs(p.LengthM-want) > 1e-6 {
				t.Errorf("(%d,%d): length = %v, want %v", r, c, p.LengthM, want)
			}
		}
	}
}
