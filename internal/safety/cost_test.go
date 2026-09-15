package safety

import (
	"math"
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// oneEdgeGraph builds a two-node graph with a single edge, for cost tests.
func oneEdgeGraph(lts uint8, infra graph.Infra, surface graph.Surface, grade float32, lit int8) *graph.Graph {
	return &graph.Graph{
		NodeLat: make([]float64, 2), NodeLon: make([]float64, 2),
		NodeControl: make([]graph.Control, 2),
		NodeMaxLTS:  []uint8{lts, lts},
		From:        []int32{0}, To: []int32{1},
		LengthM: []float32{100}, DBID: []int64{0},
		Infra: []graph.Infra{infra}, Highway: []graph.Highway{graph.HwyResidential},
		Surface:  []graph.Surface{surface},
		MaxSpeed: []int16{graph.SpeedUnknown}, Lanes: []int8{graph.LanesUnknown},
		Lit: []int8{lit}, LTS: []uint8{lts},
		CrashScore: []float32{0}, GradePct: []float32{grade},
		BearingStart: []float32{0}, BearingEnd: []float32{0},
		Offs: []int32{0, 1, 1},
	}
}

// TestMultiplierNeverBelowOne is the property the whole cost model rests on.
//
// If any edge could cost less than its own length, "effective meters" would
// stop being interpretable and straight-line distance would no longer be a
// valid A* heuristic — silently breaking Phase 7's correctness.
func TestMultiplierNeverBelowOne(t *testing.T) {
	profiles := []Profile{Cautious, Comfortable, Confident, Shortest}
	for _, p := range profiles {
		m := NewModel(p)
		for lts := uint8(0); lts <= 4; lts++ {
			for _, sfc := range []graph.Surface{
				graph.SurfaceUnknown, graph.SurfacePaved, graph.SurfaceCompacted,
				graph.SurfaceLoose, graph.SurfaceBad,
			} {
				for _, grade := range []float32{-15, -5, 0, 5, 15} {
					g := oneEdgeGraph(lts, graph.InfraNone, sfc, grade, graph.LitNo)
					if got := m.Multiplier(g, 0); got < 1.0 {
						t.Fatalf("%s: multiplier %v < 1 (lts=%d surface=%d grade=%v)",
							p.Name, got, lts, sfc, grade)
					}
				}
			}
		}
	}
}

func TestMultiplierRisesWithStress(t *testing.T) {
	m := NewModel(Comfortable)
	prev := 0.0
	for lts := uint8(1); lts <= 4; lts++ {
		g := oneEdgeGraph(lts, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
		got := m.Multiplier(g, 0)
		if got <= prev {
			t.Errorf("LTS %d multiplier %v did not exceed LTS %d's %v", lts, got, lts-1, prev)
		}
		prev = got
	}
}

// TestProtectedTrackCostsItsLength anchors the unit: the best possible
// infrastructure costs exactly its distance and nothing more.
func TestProtectedTrackCostsItsLength(t *testing.T) {
	m := NewModel(Comfortable)
	g := oneEdgeGraph(LTS1, graph.InfraProtectedTrack, graph.SurfacePaved, 0, graph.LitUnknown)

	if got := m.Multiplier(g, 0); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("multiplier = %v, want exactly 1.0", got)
	}
	if got := m.EdgeCost(g, 0); math.Abs(got-100) > 1e-9 {
		t.Errorf("cost = %v, want 100 (the edge's length)", got)
	}
}

func TestCautiousPenalisesMoreThanConfident(t *testing.T) {
	g := oneEdgeGraph(LTS3, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
	cautious := NewModel(Cautious).Multiplier(g, 0)
	confident := NewModel(Confident).Multiplier(g, 0)

	if cautious <= confident {
		t.Errorf("cautious multiplier %v should exceed confident's %v", cautious, confident)
	}
}

func TestShortestProfileIgnoresSafety(t *testing.T) {
	// The baseline profile must be pure distance, or the comparison it
	// exists to provide would be meaningless.
	m := NewModel(Shortest)
	for lts := uint8(1); lts <= 4; lts++ {
		g := oneEdgeGraph(lts, graph.InfraNone, graph.SurfaceLoose, 10, graph.LitNo)
		if got := m.Multiplier(g, 0); math.Abs(got-1.0) > 1e-9 {
			t.Errorf("LTS %d: shortest multiplier = %v, want 1.0", lts, got)
		}
	}
}

// TestZeroBudgetIsAHardFilter checks that the old strict behaviour survives
// as the special case of a rider who will not exceed their threshold at all.
func TestZeroBudgetIsAHardFilter(t *testing.T) {
	strict := Cautious
	strict.StressBudgetM = 0
	m := NewModel(strict) // MaxLTS 2, no budget

	for lts := uint8(1); lts <= 4; lts++ {
		g := oneEdgeGraph(lts, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
		allowed := m.Allow(g, 0)
		want := lts <= 2
		if allowed != want {
			t.Errorf("LTS %d: allowed = %v, want %v", lts, allowed, want)
		}
	}
}

// TestBudgetMakesEveryEdgeTraversable checks the new default: with a budget,
// nothing is forbidden outright — the budget does the limiting instead.
func TestBudgetMakesEveryEdgeTraversable(t *testing.T) {
	m := NewModel(Cautious) // MaxLTS 2, 400 m budget
	for lts := uint8(1); lts <= 4; lts++ {
		g := oneEdgeGraph(lts, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
		if !m.Allow(g, 0) {
			t.Errorf("LTS %d should be traversable when a budget exists", lts)
		}
	}
}

// TestOverIdentifiesBudgetConsumingEdges checks what draws the budget down.
func TestOverIdentifiesBudgetConsumingEdges(t *testing.T) {
	m := NewModel(Cautious) // MaxLTS 2
	for lts := uint8(1); lts <= 4; lts++ {
		g := oneEdgeGraph(lts, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
		over := m.Over(g, 0)
		want := lts > 2
		if over != want {
			t.Errorf("LTS %d: over = %v, want %v", lts, over, want)
		}
	}

	// An unscored edge must not silently consume budget.
	g := oneEdgeGraph(0, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
	if m.Over(g, 0) {
		t.Error("an unscored edge should not count against the budget")
	}
}

// TestOverThresholdIsHeavilyPenalised checks that budgeted road is expensive
// enough that the router treats it as a last resort rather than a shortcut.
func TestOverThresholdIsHeavilyPenalised(t *testing.T) {
	m := NewModel(Cautious) // MaxLTS 2
	within := oneEdgeGraph(LTS2, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
	beyond := oneEdgeGraph(LTS3, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)

	a, b := m.Multiplier(within, 0), m.Multiplier(beyond, 0)
	if b < a*5 {
		t.Errorf("over-threshold multiplier %v should dwarf in-threshold %v", b, a)
	}
}

func TestOverThresholdMTotalsExposure(t *testing.T) {
	m := NewModel(Cautious)
	g := oneEdgeGraph(LTS4, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)

	if got := m.OverThresholdM(g, []int32{0}); got != 100 {
		t.Errorf("exposure = %v, want 100 (the edge length)", got)
	}
	safe := oneEdgeGraph(LTS1, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
	if got := m.OverThresholdM(safe, []int32{0}); got != 0 {
		t.Errorf("exposure = %v, want 0", got)
	}
}

func TestGradePenaltyIsAsymmetric(t *testing.T) {
	// A 6% climb should cost more than a 6% descent: climbing is tiring,
	// and moderate descents are not hazardous.
	climb := gradePenalty(6)
	descent := gradePenalty(-6)
	if climb <= descent {
		t.Errorf("climb penalty %v should exceed descent penalty %v", climb, descent)
	}

	// Minor undulation is not worth routing around.
	if p := gradePenalty(2); p != 0 {
		t.Errorf("2%% grade penalty = %v, want 0 (inside the deadband)", p)
	}

	// But a very steep descent IS hazardous, and should outrank the same
	// gradient uphill.
	steepDown := gradePenalty(-12)
	sameUp := gradePenalty(12)
	if steepDown <= sameUp {
		t.Errorf("a 12%% descent (%v) should be penalised above a 12%% climb (%v) — braking distance and carried speed", steepDown, sameUp)
	}
}

func TestNightLightingOnlyAppliesAtNight(t *testing.T) {
	g := oneEdgeGraph(LTS2, graph.InfraNone, graph.SurfacePaved, 0, graph.LitNo)

	day := NewModel(Comfortable).Multiplier(g, 0)

	night := Comfortable
	night.Night = true
	atNight := NewModel(night).Multiplier(g, 0)

	if atNight <= day {
		t.Errorf("unlit road should cost more at night: %v vs %v", atNight, day)
	}

	// Unknown lighting must stay neutral rather than being assumed dark —
	// only about 1%% of Austin edges carry a lit tag.
	gUnknown := oneEdgeGraph(LTS2, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
	if NewModel(night).Multiplier(gUnknown, 0) != day {
		t.Error("unknown lighting should not be penalised")
	}
}

func TestScoreRangesOverTheWholeScale(t *testing.T) {
	best := oneEdgeGraph(LTS1, graph.InfraProtectedTrack, graph.SurfacePaved, 0, graph.LitUnknown)
	if s := Score(best, []int32{0}); s < 99 {
		t.Errorf("a fully protected route scored %v, want ~100", s)
	}
	worst := oneEdgeGraph(LTS4, graph.InfraNone, graph.SurfacePaved, 0, graph.LitUnknown)
	if s := Score(worst, []int32{0}); s > 1 {
		t.Errorf("an LTS 4 route scored %v, want ~0", s)
	}
}

func TestProfileValidation(t *testing.T) {
	if err := Comfortable.Validate(); err != nil {
		t.Errorf("built-in profile rejected: %v", err)
	}

	bad := Comfortable
	bad.MaxLTS = 9
	if bad.Validate() == nil {
		t.Error("max_lts of 9 should be rejected")
	}

	// A negative weight would let an edge cost less than its length, which
	// breaks the A* heuristic's correctness guarantee.
	bad = Comfortable
	bad.WeightLTS = -1
	if bad.Validate() == nil {
		t.Error("a negative weight should be rejected")
	}
}

func TestProfileByName(t *testing.T) {
	for _, name := range []string{"cautious", "comfortable", "confident", "shortest", ""} {
		if _, err := ProfileByName(name); err != nil {
			t.Errorf("ProfileByName(%q) failed: %v", name, err)
		}
	}
	if _, err := ProfileByName("reckless"); err == nil {
		t.Error("unknown profile should be an error")
	}
	// An empty name must default to comfortable, not to an empty profile
	// with every weight zero.
	p, _ := ProfileByName("")
	if p.Name != "comfortable" {
		t.Errorf("empty name gave %q, want comfortable", p.Name)
	}
}
