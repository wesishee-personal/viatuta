package safety

import "github.com/wesishee/viatuta/internal/graph"

// LTS levels. Higher is more stressful.
const (
	LTS1 uint8 = 1 // suitable for children
	LTS2 uint8 = 2 // most adults will tolerate it
	LTS3 uint8 = 3 // confident cyclists
	LTS4 uint8 = 4 // fearless riders only
)

// EdgeFacts is everything LTS classification reads. Taking a plain struct
// rather than the graph keeps the rule set testable without building a graph,
// and makes the inputs explicit.
type EdgeFacts struct {
	Infra   graph.Infra
	Highway graph.Highway
	Surface graph.Surface

	// SpeedMPH and Lanes may be graph.SpeedUnknown / graph.LanesUnknown, in
	// which case the statutory defaults for the road class are used.
	SpeedMPH int16
	Lanes    int8
}

// FactsFor extracts an edge's facts from the graph.
func FactsFor(g *graph.Graph, e int32) EdgeFacts {
	return EdgeFacts{
		Infra:    g.Infra[e],
		Highway:  g.Highway[e],
		Surface:  g.Surface[e],
		SpeedMPH: g.MaxSpeed[e],
		Lanes:    g.Lanes[e],
	}
}

// EffectiveSpeed returns the speed limit to classify with, falling back to the
// statutory default for the road class when OSM has no maxspeed tag.
//
// The fallback matters more than it sounds: only about a quarter of edges are
// tagged, so for most of the graph this function IS the speed limit.
func (f EdgeFacts) EffectiveSpeed() int16 {
	if f.SpeedMPH != graph.SpeedUnknown && f.SpeedMPH > 0 {
		return f.SpeedMPH
	}
	if d, ok := DefaultSpeedMPH[f.Highway]; ok {
		return d
	}
	return 30 // unknown road class; assume the urban default
}

// EffectiveLanes returns the lane count to classify with.
func (f EdgeFacts) EffectiveLanes() int8 {
	if f.Lanes != graph.LanesUnknown && f.Lanes > 0 {
		return f.Lanes
	}
	if d, ok := DefaultLanes[f.Highway]; ok {
		return d
	}
	return 2
}

// ClassifyLTS assigns a Level of Traffic Stress from 1 to 4.
//
// The rubric is Mekuria, Furth & Nixon (2012), simplified to the inputs OSM
// actually provides. The order of the checks is the substance of the model:
//
//  1. Physical separation from traffic decides the answer outright.
//  2. Otherwise a bike lane's value depends on the traffic beside it.
//  3. Otherwise stress comes from speed and lane count.
func ClassifyLTS(f EdgeFacts) uint8 {
	// --- 1. off-street and physically separated ---
	switch f.Infra {
	case graph.InfraProtectedTrack:
		// Separation is the point. A protected track beside an arterial is
		// LTS 1 no matter what the arterial is doing.
		return LTS1
	case graph.InfraPath:
		// Shared-use paths are low stress, but a loose surface makes one
		// unrideable for many people, which is its own kind of barrier.
		if f.Surface == graph.SurfaceLoose || f.Surface == graph.SurfaceBad {
			return LTS2
		}
		return LTS1
	case graph.InfraPedestrian:
		// Legal to ride, but crowded and slow. Not stressful in a traffic
		// sense; rated 2 to discourage routing through pedestrian areas
		// without forbidding it.
		return LTS2
	}

	speed := f.EffectiveSpeed()
	lanes := f.EffectiveLanes()

	// --- 2. on-street bike lanes ---
	switch f.Infra {
	case graph.InfraBufferedLane:
		switch {
		case speed <= 30 && lanes <= 3:
			return LTS1
		case speed <= 35:
			return LTS2
		case speed <= 45:
			return LTS3
		default:
			return LTS4
		}

	case graph.InfraPaintedLane:
		// Paint alone. Comfort drops sharply once traffic passes at 35+.
		switch {
		case speed <= 25 && lanes <= 2:
			return LTS1
		case speed <= 30 && lanes <= 3:
			return LTS2
		case speed <= 35 && lanes <= 4:
			return LTS3
		default:
			return LTS4
		}
	}

	// --- 3. mixed traffic (including sharrows) ---
	//
	// A sharrow is paint on a shared lane. It grants no space, and the
	// research is clear that it does not reduce stress, so it falls through
	// to the mixed-traffic rules with no credit.
	switch {
	case f.Highway == graph.HwyLivingStreet:
		return LTS1
	case f.Highway == graph.HwyService && speed <= 20:
		// Alleys and access roads: slow, but with poor sightlines.
		return LTS2
	}

	switch {
	case lanes >= 6:
		return LTS4
	case speed >= 45:
		return LTS4
	case speed >= 40:
		if lanes >= 4 {
			return LTS4
		}
		return LTS3
	case speed >= 35:
		if lanes >= 4 {
			return LTS4
		}
		return LTS3
	case speed >= 30:
		// The Texas residential default lands here. Two lanes at 30 mph is
		// tolerable for most adults; four lanes at 30 mph is not.
		if lanes >= 4 {
			return LTS3
		}
		return LTS2
	case speed >= 26:
		if lanes >= 4 {
			return LTS3
		}
		return LTS2
	default:
		// 25 mph or less on a narrow street: a quiet neighbourhood road.
		if lanes >= 4 {
			return LTS3
		}
		return LTS1
	}
}

// ScoreGraph classifies every edge and refreshes the per-node junction stress.
func ScoreGraph(g *graph.Graph) {
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		g.LTS[e] = ClassifyLTS(FactsFor(g, e))
	}
	g.ComputeNodeMaxLTS()
}
