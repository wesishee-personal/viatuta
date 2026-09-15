package safety

import (
	"math"

	"github.com/wesishee/viatuta/internal/graph"
)

// Model turns a Profile into the cost functions the router consumes.
type Model struct {
	Profile Profile

	// Hazards is an immutable per-edge snapshot of rider-reported hazard
	// scores, taken once when the request starts. A snapshot rather than a
	// live reference means the cost function needs no locking in the search
	// loop, and a route cannot change shape halfway through being computed
	// because someone filed a report.
	Hazards []float32
}

func NewModel(p Profile) *Model { return &Model{Profile: p} }

// WithHazards attaches a hazard snapshot.
func (m *Model) WithHazards(scores []float32) *Model {
	m.Hazards = scores
	return m
}

// hazardScore reads the snapshot defensively: a graph reloaded while a
// snapshot is in flight could leave the two different lengths.
func (m *Model) hazardScore(e int32) float64 {
	if int(e) >= len(m.Hazards) {
		return 0
	}
	return float64(m.Hazards[e])
}

// Multiplier is how much worse than a perfect protected track a meter of this
// edge is.
//
// The result is never below 1.0. That floor is load-bearing in two ways:
// it makes the cost interpretable as "effective meters" — a 5 km route
// costing 9,000 is as unpleasant as 9 km of protected track — and it
// guarantees straight-line distance is always an underestimate of remaining
// cost, which is exactly what A* needs to stay correct in Phase 7.
func (m *Model) Multiplier(g *graph.Graph, e int32) float64 {
	p := m.Profile
	mult := 1.0

	lts := g.LTS[e]
	if int(lts) < len(LTSPenalty) {
		mult += p.WeightLTS * LTSPenalty[lts]
	}

	if cs := float64(g.CrashScore[e]); cs > 0 {
		mult += p.WeightCrash * CrashPenaltyScale * cs
	}

	// Road above the rider's threshold is permitted only within their
	// budget, and is charged heavily so the router uses as little as it can.
	if lts > p.MaxLTS {
		mult += p.WeightLTS * OverThresholdPenalty
	}

	mult += p.WeightSurface * SurfacePenalty[g.Surface[e]]
	mult += p.WeightGrade * gradePenalty(float64(g.GradePct[e]))

	if hs := m.hazardScore(e); hs > 0 {
		mult += p.WeightHazard * HazardPenaltyScale * hs
	}

	if p.Night && g.Lit[e] == graph.LitNo {
		mult += p.WeightLight * UnlitPenalty
	}

	return mult
}

// EdgeCost is the router's cost function: effective meters for this edge.
func (m *Model) EdgeCost(g *graph.Graph, e int32) float64 {
	return float64(g.LengthM[e]) * m.Multiplier(g, e)
}

// Over reports whether an edge exceeds the rider's comfort threshold, and so
// draws down their stress budget.
//
// Unscored edges (LTS 0) are treated as within threshold so that a graph
// which has not been scored yet still routes; the multiplier already treats
// them as poor.
func (m *Model) Over(g *graph.Graph, e int32) bool {
	lts := g.LTS[e]
	return lts != 0 && lts > m.Profile.MaxLTS
}

// Allow implements the hard traffic-stress limit, used only when the rider
// has no stress budget at all. With a budget, every edge is traversable and
// the budget does the limiting instead.
func (m *Model) Allow(g *graph.Graph, e int32) bool {
	if m.Profile.StressBudgetM > 0 {
		return true
	}
	return !m.Over(g, e)
}

// OverThresholdM totals the distance a route spends above the rider's
// threshold — what the budget actually measures, and what the API reports so
// a rider can see the exposure rather than trust it.
func (m *Model) OverThresholdM(g *graph.Graph, edges []int32) float64 {
	var total float64
	for _, e := range edges {
		if m.Over(g, e) {
			total += float64(g.LengthM[e])
		}
	}
	return total
}

// gradePenalty charges for hills, asymmetrically.
//
// Climbing is tiring and riders route around it. Descending is fast and
// mostly pleasant — until it is steep, at which point braking distance and
// carried speed into a junction make it a genuine hazard rather than a
// reward.
func gradePenalty(pct float64) float64 {
	abs := math.Abs(pct)
	if abs <= GradeDeadbandPct {
		return 0
	}
	excess := abs - GradeDeadbandPct

	if pct > 0 {
		return excess * ClimbPenaltyPerPct
	}
	penalty := excess * DescentPenaltyPerPct
	if abs >= SteepDescentPct {
		penalty += SteepDescentPenalty
	}
	return penalty
}

// --- route quality -------------------------------------------------------

// ltsQuality maps a stress level to a 0..1 quality score, used to summarise a
// whole route in one number a rider can read.
var ltsQuality = [5]float64{0.3, 1.0, 0.8, 0.35, 0.0}

// Score summarises a route's safety from 0 (terrible) to 100 (fully
// protected), weighted by distance so a brief unavoidable arterial hurts less
// than a long one.
func Score(g *graph.Graph, edges []int32) float64 {
	var total, weighted float64
	for _, e := range edges {
		l := float64(g.LengthM[e])
		total += l
		lts := g.LTS[e]
		if int(lts) >= len(ltsQuality) {
			lts = 0
		}
		weighted += l * ltsQuality[lts]
	}
	if total == 0 {
		return 0
	}
	return 100 * weighted / total
}

// Breakdown reports how much of a route ran at each stress level and on each
// facility type. This is both the debugging tool for tuning the model and
// what lets the API explain a route rather than merely assert it.
type Breakdown struct {
	ByLTS   map[string]float64 `json:"by_lts_m"`
	ByInfra map[string]float64 `json:"by_infra_m"`

	// CrashExposureM is distance weighted by crash pressure — effectively
	// "how many metres of historically dangerous road is this route".
	CrashExposureM float64 `json:"crash_exposure_m"`

	// ClimbM and DescentM are total ascent and descent in metres.
	ClimbM   float64 `json:"climb_m"`
	DescentM float64 `json:"descent_m"`

	// SteepM is distance on gradients past the comfort deadband.
	SteepM float64 `json:"steep_m"`

	// HazardM is distance within range of an active rider-reported hazard.
	HazardM float64 `json:"hazard_m"`
}

var ltsNames = [5]string{"unknown", "lts1", "lts2", "lts3", "lts4"}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Summarise builds the per-route breakdown.
//
// This is the evidence behind the safety score. It is as much a debugging
// tool as a product feature: when a route looks wrong, these totals usually
// say which signal drove it.
func (m *Model) Summarise(g *graph.Graph, edges []int32) Breakdown {
	b := Breakdown{
		ByLTS:   map[string]float64{},
		ByInfra: map[string]float64{},
	}
	for _, e := range edges {
		l := float64(g.LengthM[e])

		lts := g.LTS[e]
		if int(lts) >= len(ltsNames) {
			lts = 0
		}
		b.ByLTS[ltsNames[lts]] += l
		b.ByInfra[g.Infra[e].String()] += l

		b.CrashExposureM += l * float64(g.CrashScore[e])

		// Ascent and descent are tracked separately because they are not
		// interchangeable: a route with 200 m of climbing is hard work,
		// while one with 200 m of steep descent is a braking problem.
		rise := float64(g.GradePct[e]) / 100 * l
		if rise > 0 {
			b.ClimbM += rise
		} else {
			b.DescentM += -rise
		}
		if abs(float64(g.GradePct[e])) > GradeDeadbandPct {
			b.SteepM += l
		}
		if m.hazardScore(e) > 0 {
			b.HazardM += l
		}
	}
	b.CrashExposureM = math.Round(b.CrashExposureM*10) / 10
	b.ClimbM = math.Round(b.ClimbM*10) / 10
	b.DescentM = math.Round(b.DescentM*10) / 10
	b.SteepM = math.Round(b.SteepM*10) / 10
	b.HazardM = math.Round(b.HazardM*10) / 10
	for k, v := range b.ByLTS {
		b.ByLTS[k] = math.Round(v*10) / 10
	}
	for k, v := range b.ByInfra {
		b.ByInfra[k] = math.Round(v*10) / 10
	}
	return b
}
