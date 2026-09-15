package routing

import (
	"math/rand"
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// benchGraph builds a grid roughly the size of a metro street network.
// 200x200 is 40,000 nodes and about 159,000 directed edges.
func benchGraph(b *testing.B) *graph.Graph {
	b.Helper()
	return geoGrid(200, 42)
}

func benchOpts(g *graph.Graph) Options {
	// A cost function shaped like the real safety model: a multiplier of at
	// least 1.0, plus turn costs.
	return Options{
		EdgeCost: func(g *graph.Graph, e int32) float64 {
			return float64(g.LengthM[e]) * (1 + float64(e%7)*0.4)
		},
		TurnCost: func(g *graph.Graph, in, out int32) float64 {
			if g.IsReverseOf(in, out) {
				return 300
			}
			return 8
		},
	}
}

func BenchmarkDijkstra(b *testing.B) {
	g := benchGraph(b)
	opt := benchOpts(g)
	opt.NoHeuristic = true
	start, goal := int32(0), int32(g.NumNodes()-1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err := Search(g, start, goal, opt)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(p.Expansions), "states/op")
	}
}

func BenchmarkAStar(b *testing.B) {
	g := benchGraph(b)
	opt := benchOpts(g)
	start, goal := int32(0), int32(g.NumNodes()-1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err := Search(g, start, goal, opt)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(p.Expansions), "states/op")
	}
}

// BenchmarkAStarWithBudget measures the cost of the second state dimension.
func BenchmarkAStarWithBudget(b *testing.B) {
	g := benchGraph(b)
	opt := benchOpts(g)
	opt.Over = func(g *graph.Graph, e int32) bool { return e%5 == 0 }
	opt.BudgetM = 400
	start, goal := int32(0), int32(g.NumNodes()-1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Search(g, start, goal, opt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAStarShortTrip reflects the common case: most riders are not
// crossing the whole city.
func BenchmarkAStarShortTrip(b *testing.B) {
	g := benchGraph(b)
	opt := benchOpts(g)
	rng := rand.New(rand.NewSource(1))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := int32(rng.Intn(g.NumNodes()))
		goal := start + 25*200 + 25 // roughly 25 cells away in each direction
		if int(goal) >= g.NumNodes() {
			goal = start
			continue
		}
		if _, err := Search(g, start, goal, opt); err != nil {
			b.Fatal(err)
		}
	}
}
