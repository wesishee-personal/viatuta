package safety

import (
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// turnGraph builds a junction: edge 0 arrives at node 1, edges 1 and 2 leave
// it. Bearings are set directly so turn geometry is exact.
func turnGraph(inBearingEnd, outBearingStart float32, control graph.Control, nodeLTS uint8,
	inInfra, outInfra graph.Infra) *graph.Graph {

	g := &graph.Graph{
		NodeLat: make([]float64, 3), NodeLon: make([]float64, 3),
		NodeControl: []graph.Control{graph.ControlNone, control, graph.ControlNone},
		NodeMaxLTS:  []uint8{nodeLTS, nodeLTS, nodeLTS},
		From:        []int32{0, 1}, To: []int32{1, 2},
		LengthM: []float32{100, 100}, DBID: []int64{0, 1},
		Infra:      []graph.Infra{inInfra, outInfra},
		Highway:    []graph.Highway{graph.HwyResidential, graph.HwyResidential},
		Surface:    []graph.Surface{graph.SurfacePaved, graph.SurfacePaved},
		MaxSpeed:   []int16{graph.SpeedUnknown, graph.SpeedUnknown},
		Lanes:      []int8{graph.LanesUnknown, graph.LanesUnknown},
		Lit:        []int8{graph.LitUnknown, graph.LitUnknown},
		LTS:        []uint8{nodeLTS, nodeLTS},
		CrashScore: []float32{0, 0}, GradePct: []float32{0, 0},
		BearingStart: []float32{0, outBearingStart},
		BearingEnd:   []float32{inBearingEnd, 0},
		Offs:         []int32{0, 1, 2, 2},
	}
	return g
}

func TestClassifyTurn(t *testing.T) {
	cases := []struct {
		name         string
		inEnd, outIn float32
		want         TurnKind
	}{
		{"continue straight north", 0, 0, TurnStraight},
		{"slight bend is still straight", 0, 20, TurnStraight},
		{"right turn", 0, 90, TurnRight},
		{"left turn", 0, 270, TurnLeft},
		{"left turn across the north wrap", 10, 300, TurnLeft},
		{"right turn across the north wrap", 350, 60, TurnRight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := turnGraph(tc.inEnd, tc.outIn, graph.ControlNone, 2, graph.InfraNone, graph.InfraNone)
			if got := ClassifyTurn(g, 0, 1); got != tc.want {
				t.Errorf("ClassifyTurn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUTurnIsDetectedFromTopology(t *testing.T) {
	// Two edges that are the same road in opposite directions.
	g := &graph.Graph{
		NodeLat: make([]float64, 2), NodeLon: make([]float64, 2),
		NodeControl: make([]graph.Control, 2),
		NodeMaxLTS:  []uint8{2, 2},
		From:        []int32{0, 1}, To: []int32{1, 0},
		LengthM: []float32{100, 100}, DBID: []int64{0, 1},
		Infra:      []graph.Infra{graph.InfraNone, graph.InfraNone},
		Highway:    []graph.Highway{graph.HwyResidential, graph.HwyResidential},
		Surface:    []graph.Surface{graph.SurfacePaved, graph.SurfacePaved},
		MaxSpeed:   []int16{graph.SpeedUnknown, graph.SpeedUnknown},
		Lanes:      []int8{graph.LanesUnknown, graph.LanesUnknown},
		Lit:        []int8{graph.LitUnknown, graph.LitUnknown},
		LTS:        []uint8{2, 2},
		CrashScore: []float32{0, 0}, GradePct: []float32{0, 0},
		BearingStart: []float32{0, 180}, BearingEnd: []float32{0, 180},
		Offs: []int32{0, 1, 2},
	}
	if got := ClassifyTurn(g, 0, 1); got != TurnUTurn {
		t.Errorf("ClassifyTurn = %v, want u-turn", got)
	}
}

// TestLeftTurnsCostMoreThanRight is the asymmetry that matters in right-hand
// traffic: a left turn crosses opposing lanes.
func TestLeftTurnsCostMoreThanRight(t *testing.T) {
	m := NewModel(Comfortable)

	left := turnGraph(0, 270, graph.ControlNone, 3, graph.InfraNone, graph.InfraNone)
	right := turnGraph(0, 90, graph.ControlNone, 3, graph.InfraNone, graph.InfraNone)
	straight := turnGraph(0, 0, graph.ControlNone, 3, graph.InfraNone, graph.InfraNone)

	lc, rc, sc := m.TurnCost(left, 0, 1), m.TurnCost(right, 0, 1), m.TurnCost(straight, 0, 1)

	if !(lc > rc && rc > sc) {
		t.Errorf("expected left > right > straight, got %v, %v, %v", lc, rc, sc)
	}
}

// TestSignalReducesTurnCost checks the control term. A signal does not remove
// the risk, but it removes the gap-acceptance decision.
func TestSignalReducesTurnCost(t *testing.T) {
	m := NewModel(Comfortable)
	uncontrolled := turnGraph(0, 270, graph.ControlNone, 4, graph.InfraNone, graph.InfraNone)
	signalised := turnGraph(0, 270, graph.ControlSignal, 4, graph.InfraNone, graph.InfraNone)

	u, s := m.TurnCost(uncontrolled, 0, 1), m.TurnCost(signalised, 0, 1)
	if s >= u {
		t.Errorf("signalised left (%v) should cost less than uncontrolled (%v)", s, u)
	}
}

// TestCrossingBusyRoadsCostsMore checks that the junction's worst stress
// scales the manoeuvre.
func TestCrossingBusyRoadsCostsMore(t *testing.T) {
	m := NewModel(Comfortable)
	quiet := turnGraph(0, 270, graph.ControlNone, 1, graph.InfraNone, graph.InfraNone)
	busy := turnGraph(0, 270, graph.ControlNone, 4, graph.InfraNone, graph.InfraNone)

	if m.TurnCost(busy, 0, 1) <= m.TurnCost(quiet, 0, 1) {
		t.Error("turning across an LTS 4 road should cost more than across an LTS 1 road")
	}
}

// TestProtectedContinuationIsNearlyFree checks that staying on separated
// infrastructure through a junction avoids the conflict being modelled.
func TestProtectedContinuationIsNearlyFree(t *testing.T) {
	m := NewModel(Comfortable)
	onStreet := turnGraph(0, 270, graph.ControlNone, 4, graph.InfraNone, graph.InfraNone)
	onTrail := turnGraph(0, 270, graph.ControlNone, 4,
		graph.InfraProtectedTrack, graph.InfraProtectedTrack)

	if m.TurnCost(onTrail, 0, 1) >= m.TurnCost(onStreet, 0, 1) {
		t.Error("a turn along a protected trail should cost far less than the same turn in traffic")
	}
}

func TestUTurnIsHeavilyPenalised(t *testing.T) {
	m := NewModel(Comfortable)
	g := &graph.Graph{
		NodeLat: make([]float64, 2), NodeLon: make([]float64, 2),
		NodeControl: make([]graph.Control, 2), NodeMaxLTS: []uint8{1, 1},
		From: []int32{0, 1}, To: []int32{1, 0},
		LengthM: []float32{100, 100}, DBID: []int64{0, 1},
		Infra:      []graph.Infra{graph.InfraNone, graph.InfraNone},
		Highway:    []graph.Highway{graph.HwyResidential, graph.HwyResidential},
		Surface:    []graph.Surface{graph.SurfacePaved, graph.SurfacePaved},
		MaxSpeed:   []int16{graph.SpeedUnknown, graph.SpeedUnknown},
		Lanes:      []int8{graph.LanesUnknown, graph.LanesUnknown},
		Lit:        []int8{graph.LitUnknown, graph.LitUnknown},
		LTS:        []uint8{1, 1},
		CrashScore: []float32{0, 0}, GradePct: []float32{0, 0},
		BearingStart: []float32{0, 180}, BearingEnd: []float32{0, 180},
		Offs: []int32{0, 1, 2},
	}
	// Even on the quietest possible street, a u-turn must dominate any
	// ordinary turn on the worst possible street.
	worstOrdinary := m.TurnCost(
		turnGraph(0, 270, graph.ControlNone, 4, graph.InfraNone, graph.InfraNone), 0, 1)
	if m.TurnCost(g, 0, 1) <= worstOrdinary {
		t.Error("a u-turn should cost more than the worst ordinary turn")
	}
}

func TestTurnWeightScalesWithProfile(t *testing.T) {
	g := turnGraph(0, 270, graph.ControlNone, 4, graph.InfraNone, graph.InfraNone)
	cautious := NewModel(Cautious).TurnCost(g, 0, 1)
	confident := NewModel(Confident).TurnCost(g, 0, 1)

	if cautious <= confident {
		t.Errorf("cautious turn cost %v should exceed confident's %v", cautious, confident)
	}
}
