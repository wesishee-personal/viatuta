package safety

import (
	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
)

// TurnKind classifies a movement through a junction.
type TurnKind uint8

const (
	TurnStraight TurnKind = iota
	TurnRight
	TurnLeft
	TurnUTurn
)

func (t TurnKind) String() string {
	switch t {
	case TurnRight:
		return "right"
	case TurnLeft:
		return "left"
	case TurnUTurn:
		return "u-turn"
	default:
		return "straight"
	}
}

// ClassifyTurn determines what kind of movement leads from edge `in` to edge
// `out`.
//
// The sign convention comes from geo.TurnAngleDeg: negative is a left turn.
// Left turns are kept distinct from right ones because in right-hand traffic
// a left turn crosses opposing lanes, which is where cyclists get hit.
func ClassifyTurn(g *graph.Graph, in, out int32) TurnKind {
	if g.IsReverseOf(in, out) {
		return TurnUTurn
	}

	angle := geo.TurnAngleDeg(float64(g.BearingEnd[in]), float64(g.BearingStart[out]))
	switch {
	case angle <= -StraightAngleDeg:
		return TurnLeft
	case angle >= StraightAngleDeg:
		return TurnRight
	default:
		return TurnStraight
	}
}

// TurnCost returns the cost of a movement, in effective meters.
//
// Three things scale the base cost of the manoeuvre:
//
//   - what is being crossed, via the junction's worst traffic stress;
//   - how the junction is controlled, since a signal removes the
//     gap-acceptance decision that makes unsignalised left turns dangerous;
//   - whether the rider stays on separated infrastructure throughout, in
//     which case the conflict being modelled is simply not present.
func (m *Model) TurnCost(g *graph.Graph, in, out int32) float64 {
	kind := ClassifyTurn(g, in, out)
	if kind == TurnUTurn {
		return m.Profile.WeightTurn * TurnUTurnM
	}

	var base float64
	switch kind {
	case TurnLeft:
		base = TurnLeftM
	case TurnRight:
		base = TurnRightM
	default:
		base = TurnStraightM
	}

	node := g.To[in]

	stress := CrossingStressFactor[0]
	if int(g.NodeMaxLTS[node]) < len(CrossingStressFactor) {
		stress = CrossingStressFactor[g.NodeMaxLTS[node]]
	}

	control, ok := ControlFactor[g.NodeControl[node]]
	if !ok {
		control = 1.0
	}

	cost := base * stress * control

	// Staying on separated infrastructure through the junction — a trail
	// crossing, or a protected intersection — means the conflict the base
	// cost represents does not arise.
	if isSeparated(g.Infra[in]) && isSeparated(g.Infra[out]) {
		cost *= ProtectedContinuationFactor
	}

	return m.Profile.WeightTurn * cost
}

func isSeparated(i graph.Infra) bool {
	return i == graph.InfraProtectedTrack || i == graph.InfraPath
}
