// Package safety decides what things cost.
//
// This is the core of the project. internal/routing knows how to find a cheap
// path through a graph but has no idea what a bike lane is; this package
// supplies the cost functions that make "cheap" mean "safe". Retuning the
// entire safety model means editing this package and nothing else.
//
// Every constant in this file is a judgement call. docs/safety-model.md
// records the reasoning; if you change a number here, change the reasoning
// there too. A safety model nobody can explain is one nobody should trust.
package safety

import "github.com/wesishee/viatuta/internal/graph"

// --- Level of Traffic Stress penalties -----------------------------------

// LTSPenalty is the cost multiplier added per unit weight, indexed by LTS.
//
// Read these as "how much further would a rider go to avoid a meter of this?"
// An LTS 4 arterial at weight 1.0 costs 6x its length, so the router will
// take a 600 m detour to avoid 100 m of it. That is aggressive on purpose:
// the premise of this project is that safety dominates.
//
// The jump from LTS 2 to LTS 3 is deliberately large. That is the boundary
// where a road stops being usable by an ordinary adult on a bike, and it is
// the single most important threshold in the model.
var LTSPenalty = [5]float64{
	3.0, // index 0: unknown — treated as poor, so unscored edges are avoided
	0.0, // LTS 1: protected or genuinely quiet; the baseline
	0.35,
	1.6,
	5.0, // LTS 4
}

// OverThresholdPenalty is the extra multiplier charged for a meter of road
// above the rider's stated comfort threshold.
//
// It is deliberately brutal. Combined with a bounded budget, the effect is
// that the router treats over-threshold road as a last resort, uses the
// shortest possible stretch of it, and never spends more than the rider
// allowed. The alternative — a hard wall — confines a cautious rider to 19%
// of Austin, because the low-stress network is islanded.
const OverThresholdPenalty = 12.0

// --- speed defaults ------------------------------------------------------

// DefaultSpeedMPH supplies a speed limit when OSM has none, which is the case
// for roughly three quarters of edges. These are Texas statutory defaults,
// not guesses.
//
// Note that residential is 30, not the 20-25 common in other states. Cyclist
// fatality risk climbs steeply across that range, so an untagged Austin
// residential street must NOT be treated as automatically low-stress.
var DefaultSpeedMPH = map[graph.Highway]int16{
	graph.HwyLivingStreet: 15,
	graph.HwyService:      15,
	graph.HwyResidential:  30,
	graph.HwyUnclassified: 30,
	graph.HwyTertiary:     35,
	graph.HwySecondary:    40,
	graph.HwyPrimary:      45,
	graph.HwyTrunk:        55,
}

// DefaultLanes supplies a lane count when OSM has none. Lane count is the
// second input to LTS after speed, and is missing on three quarters of edges.
var DefaultLanes = map[graph.Highway]int8{
	graph.HwyLivingStreet: 2,
	graph.HwyService:      2,
	graph.HwyResidential:  2,
	graph.HwyUnclassified: 2,
	graph.HwyTertiary:     2,
	graph.HwySecondary:    4,
	graph.HwyPrimary:      4,
	graph.HwyTrunk:        4,
}

// --- surface -------------------------------------------------------------

// SurfacePenalty is added per unit weight for rough surfaces. Loose gravel is
// both slower and a genuine loss-of-control risk on narrow tyres.
var SurfacePenalty = map[graph.Surface]float64{
	graph.SurfaceUnknown:   0.0, // unknown stays neutral, never assumed bad
	graph.SurfacePaved:     0.0,
	graph.SurfaceCompacted: 0.25,
	graph.SurfaceLoose:     1.2,
	graph.SurfaceBad:       4.0,
}

// --- grade ---------------------------------------------------------------

// Grade penalties are asymmetric: a climb is tiring, but a steep descent is
// genuinely hazardous — braking distance grows and a rider carries speed into
// junctions. Below GradeDeadbandPct nothing is charged, because minor
// undulation is not worth routing around.
const (
	GradeDeadbandPct = 3.0

	// Per percent of grade beyond the deadband.
	ClimbPenaltyPerPct   = 0.12
	DescentPenaltyPerPct = 0.06

	// Beyond this, a descent stops being fast and starts being dangerous.
	SteepDescentPct     = 8.0
	SteepDescentPenalty = 0.6
)

// --- crash pressure ------------------------------------------------------

// CrashPenaltyScale converts the normalised 0..1 crash score into cost. Kept
// well below the LTS penalties on purpose: crash data is exposure-blind, so
// it corrects the road-geometry model rather than replacing it.
const CrashPenaltyScale = 2.5

// --- lighting ------------------------------------------------------------

// UnlitPenalty applies only to night-time requests, and only where OSM says a
// way is explicitly unlit. Unknown lighting stays neutral — which, given that
// only about 1% of Austin edges carry a `lit` tag, means this term does very
// little until a streetlight dataset is imported.
const UnlitPenalty = 0.8

// --- rider-reported hazards ----------------------------------------------

// HazardPenaltyScale converts a 0..1 hazard score into cost. Held below the
// crash scale on purpose: hazard reports are unverified and a single rider
// can file one, so they nudge routing rather than dictate it.
const HazardPenaltyScale = 1.5

// --- turn costs ----------------------------------------------------------

// Turn costs are in effective meters, directly comparable with edge costs.
//
// Most cyclist injuries happen at junctions rather than mid-block, so these
// are first-class terms. A left turn costs four times a right turn because in
// right-hand traffic it means crossing opposing lanes.
const (
	TurnStraightM = 4.0
	TurnRightM    = 10.0
	TurnLeftM     = 40.0

	// A u-turn is almost never what a rider wants, and a route containing
	// one usually indicates a graph artifact rather than a real manoeuvre.
	TurnUTurnM = 300.0

	// Angles narrower than this count as continuing straight.
	StraightAngleDeg = 35.0
)

// CrossingStressFactor scales a turn by the stress of the busiest road at the
// junction — the road being crossed. Turning off a quiet street onto another
// quiet street is nearly free; the same manoeuvre across four lanes is not.
var CrossingStressFactor = [5]float64{
	1.5, // unknown
	0.3, // LTS 1
	0.6,
	1.5,
	4.0, // LTS 4
}

// ControlFactor scales a turn by how the junction is controlled. A signal
// does not remove risk, but it removes the gap-acceptance decision that makes
// unsignalised left turns dangerous.
var ControlFactor = map[graph.Control]float64{
	graph.ControlNone:       1.0,
	graph.ControlSignal:     0.25,
	graph.ControlStop:       0.7,
	graph.ControlYield:      0.85,
	graph.ControlCrossing:   0.5,
	graph.ControlRoundabout: 0.8,
}

// ProtectedContinuationFactor applies when a rider stays on separated
// infrastructure through a junction — a trail crossing, or a protected
// intersection. The conflict the turn cost models simply is not present.
const ProtectedContinuationFactor = 0.15
