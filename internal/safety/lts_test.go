package safety

import (
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

func TestClassifyLTS(t *testing.T) {
	cases := []struct {
		name  string
		facts EdgeFacts
		want  uint8
	}{
		// --- separation decides the answer outright ---
		{"protected track beside a fast arterial",
			EdgeFacts{Infra: graph.InfraProtectedTrack, Highway: graph.HwyPrimary, SpeedMPH: 45, Lanes: 6}, LTS1},
		{"paved shared-use path",
			EdgeFacts{Infra: graph.InfraPath, Highway: graph.HwyPath, Surface: graph.SurfacePaved}, LTS1},
		{"gravel path is a barrier for many riders",
			EdgeFacts{Infra: graph.InfraPath, Highway: graph.HwyPath, Surface: graph.SurfaceLoose}, LTS2},

		// --- bike lanes depend on the traffic beside them ---
		{"buffered lane on a 30 mph street",
			EdgeFacts{Infra: graph.InfraBufferedLane, Highway: graph.HwySecondary, SpeedMPH: 30, Lanes: 2}, LTS1},
		{"buffered lane on a 45 mph arterial",
			EdgeFacts{Infra: graph.InfraBufferedLane, Highway: graph.HwyPrimary, SpeedMPH: 45, Lanes: 4}, LTS3},
		{"painted lane on a quiet street",
			EdgeFacts{Infra: graph.InfraPaintedLane, Highway: graph.HwyResidential, SpeedMPH: 25, Lanes: 2}, LTS1},
		{"painted lane on a 40 mph arterial is not enough",
			EdgeFacts{Infra: graph.InfraPaintedLane, Highway: graph.HwyPrimary, SpeedMPH: 40, Lanes: 4}, LTS4},

		// --- mixed traffic ---
		{"living street", EdgeFacts{Highway: graph.HwyLivingStreet}, LTS1},
		{"quiet 25 mph residential",
			EdgeFacts{Highway: graph.HwyResidential, SpeedMPH: 25, Lanes: 2}, LTS1},
		{"Texas default 30 mph residential",
			EdgeFacts{Highway: graph.HwyResidential, SpeedMPH: 30, Lanes: 2}, LTS2},
		{"35 mph two-lane collector",
			EdgeFacts{Highway: graph.HwyTertiary, SpeedMPH: 35, Lanes: 2}, LTS3},
		{"45 mph arterial, no facility",
			EdgeFacts{Highway: graph.HwyPrimary, SpeedMPH: 45, Lanes: 4}, LTS4},
		{"six lanes is stressful at any speed",
			EdgeFacts{Highway: graph.HwyPrimary, SpeedMPH: 30, Lanes: 6}, LTS4},

		// A sharrow is paint on a shared lane. It grants no space, so it
		// must score exactly the same as no facility at all.
		{"sharrow gets no credit",
			EdgeFacts{Infra: graph.InfraSharedLane, Highway: graph.HwyTertiary, SpeedMPH: 35, Lanes: 2}, LTS3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyLTS(tc.facts); got != tc.want {
				t.Errorf("ClassifyLTS = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSharrowMatchesMixedTraffic states the equivalence directly, since it is
// a claim about the world rather than an arbitrary table entry.
func TestSharrowMatchesMixedTraffic(t *testing.T) {
	base := EdgeFacts{Highway: graph.HwyTertiary, SpeedMPH: 35, Lanes: 2}
	withSharrow := base
	withSharrow.Infra = graph.InfraSharedLane

	if ClassifyLTS(base) != ClassifyLTS(withSharrow) {
		t.Error("a sharrow must not change the stress level; it grants no space")
	}
}

func TestSpeedFallbacks(t *testing.T) {
	// Three quarters of edges have no maxspeed tag, so the fallback is the
	// speed limit for most of the graph.
	f := EdgeFacts{Highway: graph.HwyResidential, SpeedMPH: graph.SpeedUnknown}
	if got := f.EffectiveSpeed(); got != 30 {
		t.Errorf("untagged residential = %d mph, want the Texas urban default of 30", got)
	}

	f = EdgeFacts{Highway: graph.HwyPrimary, SpeedMPH: graph.SpeedUnknown}
	if got := f.EffectiveSpeed(); got != 45 {
		t.Errorf("untagged primary = %d mph, want 45", got)
	}

	// A real tag always wins over the default.
	f = EdgeFacts{Highway: graph.HwyResidential, SpeedMPH: 20}
	if got := f.EffectiveSpeed(); got != 20 {
		t.Errorf("tagged speed = %d, want 20", got)
	}
}

func TestLaneFallbacks(t *testing.T) {
	f := EdgeFacts{Highway: graph.HwyPrimary, Lanes: graph.LanesUnknown}
	if got := f.EffectiveLanes(); got != 4 {
		t.Errorf("untagged primary = %d lanes, want 4", got)
	}
	f = EdgeFacts{Highway: graph.HwyResidential, Lanes: graph.LanesUnknown}
	if got := f.EffectiveLanes(); got != 2 {
		t.Errorf("untagged residential = %d lanes, want 2", got)
	}
}

// TestLTSIsMonotonicInSpeed guards the central property of the rubric: making
// traffic faster must never make a road less stressful.
func TestLTSIsMonotonicInSpeed(t *testing.T) {
	for _, infra := range []graph.Infra{
		graph.InfraNone, graph.InfraPaintedLane, graph.InfraBufferedLane,
	} {
		prev := uint8(0)
		for speed := int16(15); speed <= 60; speed += 5 {
			got := ClassifyLTS(EdgeFacts{
				Infra: infra, Highway: graph.HwyTertiary, SpeedMPH: speed, Lanes: 2,
			})
			if got < prev {
				t.Errorf("infra %d: LTS dropped from %d to %d as speed rose to %d mph",
					infra, prev, got, speed)
			}
			prev = got
		}
	}
}

// TestBetterInfraNeverScoresWorse checks the other direction: upgrading the
// facility on an identical road must never raise stress.
func TestBetterInfraNeverScoresWorse(t *testing.T) {
	order := []graph.Infra{
		graph.InfraNone, graph.InfraSharedLane, graph.InfraPaintedLane,
		graph.InfraBufferedLane, graph.InfraProtectedTrack,
	}
	for _, speed := range []int16{20, 30, 35, 45, 55} {
		prev := uint8(5)
		for _, infra := range order {
			got := ClassifyLTS(EdgeFacts{
				Infra: infra, Highway: graph.HwySecondary, SpeedMPH: speed, Lanes: 2,
			})
			if got > prev {
				t.Errorf("at %d mph: upgrading to infra %d raised LTS from %d to %d",
					speed, infra, prev, got)
			}
			prev = got
		}
	}
}

func TestEveryEdgeGetsAValidLevel(t *testing.T) {
	for _, infra := range []graph.Infra{
		graph.InfraNone, graph.InfraPedestrian, graph.InfraSharedLane,
		graph.InfraPaintedLane, graph.InfraBufferedLane, graph.InfraPath,
		graph.InfraProtectedTrack,
	} {
		for _, hwy := range []graph.Highway{
			graph.HwyOther, graph.HwyService, graph.HwyResidential,
			graph.HwyTertiary, graph.HwySecondary, graph.HwyPrimary, graph.HwyTrunk,
		} {
			for _, speed := range []int16{graph.SpeedUnknown, 15, 30, 45, 70} {
				got := ClassifyLTS(EdgeFacts{Infra: infra, Highway: hwy, SpeedMPH: speed, Lanes: graph.LanesUnknown})
				if got < 1 || got > 4 {
					t.Fatalf("infra=%d hwy=%d speed=%d produced LTS %d, outside 1..4",
						infra, hwy, speed, got)
				}
			}
		}
	}
}
