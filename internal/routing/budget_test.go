package routing

import (
	"math"
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// budgetGraph builds a corridor where the direct route crosses one stressful
// segment and the alternative is a long detour that avoids it.
//
//	0 --[safe 10]--> 1 --[STRESSFUL len]--> 2 --[safe 10]--> 3
//	0 --------------[safe detour 1000]--------------------> 3
func budgetGraph(stressLen float32) (*graph.Graph, map[int32]bool) {
	edges := []testEdge{
		{0, 1, 10},
		{1, 2, stressLen},
		{2, 3, 10},
		{0, 3, 1000},
	}
	g := buildGraph(4, edges)

	stressful := map[int32]bool{}
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		if g.From[e] == 1 && g.To[e] == 2 {
			stressful[e] = true
		}
	}
	return g, stressful
}

func budgetOpts(stressful map[int32]bool, budget float64) Options {
	return Options{
		EdgeCost: func(g *graph.Graph, e int32) float64 {
			c := float64(g.LengthM[e])
			if stressful[e] {
				c *= 12 // the over-threshold penalty
			}
			return c
		},
		Over:    func(g *graph.Graph, e int32) bool { return stressful[e] },
		BudgetM: budget,
	}
}

// TestBudgetZeroIsAHardFilter confirms the old behaviour survives as the
// special case of a budget of nothing.
func TestBudgetZeroIsAHardFilter(t *testing.T) {
	g, stressful := budgetGraph(50)

	opts := budgetOpts(stressful, 0)
	opts.Allow = func(g *graph.Graph, e int32) bool { return !stressful[e] }

	p, err := Search(g, 0, 3, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.LengthM-1000) > 1e-6 {
		t.Errorf("length = %v, want the 1000 m detour", p.LengthM)
	}
}

// TestBudgetAllowsShortCrossing is the whole point: a rider will take one bad
// block rather than a kilometre detour.
func TestBudgetAllowsShortCrossing(t *testing.T) {
	g, stressful := budgetGraph(50)

	p, err := Search(g, 0, 3, budgetOpts(stressful, 400))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.LengthM-70) > 1e-6 {
		t.Errorf("length = %v, want 70 (10 + 50 stressful + 10)", p.LengthM)
	}
	if math.Abs(p.BudgetUsedM-50) > 1e-6 {
		t.Errorf("budget used = %v, want 50", p.BudgetUsedM)
	}
}

// TestBudgetRefusesLongCrossing is the other half: it must be a real bound,
// not a preference. A 600 m stressful stretch exceeds a 400 m budget, so the
// detour wins however expensive it is.
func TestBudgetRefusesLongCrossing(t *testing.T) {
	g, stressful := budgetGraph(600)

	p, err := Search(g, 0, 3, budgetOpts(stressful, 400))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.LengthM-1000) > 1e-6 {
		t.Errorf("length = %v, want the 1000 m detour; the budget must bound exposure", p.LengthM)
	}
	if p.BudgetUsedM != 0 {
		t.Errorf("budget used = %v, want 0", p.BudgetUsedM)
	}
}

// TestBudgetIsNeverExceeded walks a chain of stressful segments and checks
// the bound holds for every budget, which is the guarantee the API promises.
func TestBudgetIsNeverExceeded(t *testing.T) {
	// A chain of five 100 m stressful edges, with no alternative.
	var edges []testEdge
	for i := int32(0); i < 5; i++ {
		edges = append(edges, testEdge{i, i + 1, 100})
	}
	g := buildGraph(6, edges)

	stressful := map[int32]bool{}
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		stressful[e] = true
	}

	for _, budget := range []float64{0, 100, 250, 400, 500, 1000} {
		opts := budgetOpts(stressful, budget)
		if budget == 0 {
			opts.Allow = func(g *graph.Graph, e int32) bool { return !stressful[e] }
		}
		p, err := Search(g, 0, 5, opts)

		if budget < 500 {
			if err == nil {
				t.Errorf("budget %v: expected failure, got a route using %v m", budget, p.BudgetUsedM)
			}
			continue
		}
		if err != nil {
			t.Errorf("budget %v: unexpected error %v", budget, err)
			continue
		}
		if p.BudgetUsedM > budget+1e-6 {
			t.Errorf("budget %v exceeded: route used %v m", budget, p.BudgetUsedM)
		}
	}
}

// TestBudgetPrefersCheaperRouteWithinBudget checks the search still optimises
// cost, rather than merely finding any feasible route.
func TestBudgetPrefersCheaperRouteWithinBudget(t *testing.T) {
	// Two stressful crossings are available: a short one and a long one.
	// Both fit the budget; the short one must win.
	edges := []testEdge{
		{0, 1, 10},
		{1, 4, 30},  // short stressful crossing
		{1, 5, 200}, // long stressful crossing
		{4, 3, 10},
		{5, 3, 10},
	}
	g := buildGraph(6, edges)

	stressful := map[int32]bool{}
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		if (g.From[e] == 1 && g.To[e] == 4) || (g.From[e] == 1 && g.To[e] == 5) {
			stressful[e] = true
		}
	}

	p, err := Search(g, 0, 3, budgetOpts(stressful, 400))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(p.BudgetUsedM-30) > 1e-6 {
		t.Errorf("budget used = %v, want 30 (the shorter crossing)", p.BudgetUsedM)
	}
}

// TestBudgetSpendingIsCumulative confirms the budget is a whole-route bound
// rather than a per-edge one — the property that distinguishes it from simply
// capping individual segment lengths.
func TestBudgetSpendingIsCumulative(t *testing.T) {
	// Three separate 150 m stressful crossings on the only path: 450 m
	// total, which fits a 500 m budget but not a 400 m one.
	edges := []testEdge{
		{0, 1, 150}, {1, 2, 10},
		{2, 3, 150}, {3, 4, 10},
		{4, 5, 150},
	}
	g := buildGraph(6, edges)

	stressful := map[int32]bool{}
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		if g.LengthM[e] == 150 {
			stressful[e] = true
		}
	}

	if _, err := Search(g, 0, 5, budgetOpts(stressful, 400)); err == nil {
		t.Error("450 m of crossings should not fit a 400 m budget")
	}

	p, err := Search(g, 0, 5, budgetOpts(stressful, 500))
	if err != nil {
		t.Fatalf("500 m budget should suffice: %v", err)
	}
	if math.Abs(p.BudgetUsedM-450) > 1e-6 {
		t.Errorf("budget used = %v, want 450", p.BudgetUsedM)
	}
}

// TestNoBudgetCollapsesToPlainDijkstra guards the fast path: without a
// budget the second state dimension must disappear entirely.
func TestNoBudgetCollapsesToPlainDijkstra(t *testing.T) {
	g, _ := budgetGraph(50)

	plain, err := Search(g, 0, 3, lengthOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(plain.LengthM-70) > 1e-6 {
		t.Errorf("length = %v, want 70", plain.LengthM)
	}
	if plain.BudgetUsedM != 0 {
		t.Errorf("budget used = %v, want 0 when no budget is configured", plain.BudgetUsedM)
	}
}
