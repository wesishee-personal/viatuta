package osm

import (
	"testing"

	"github.com/paulmach/osm"
)

func TestRoutable(t *testing.T) {
	cases := []struct {
		name string
		tags osm.Tags
		want bool
	}{
		// --- ordinary roads ---
		{"residential street", tags("highway", "residential"), true},
		{"arterial", tags("highway", "primary"), true},
		{"service road", tags("highway", "service"), true},
		{"dedicated cycleway", tags("highway", "cycleway"), true},

		// --- never routable ---
		{"motorway", tags("highway", "motorway"), false},
		{"motorway link", tags("highway", "motorway_link"), false},
		{"stairs", tags("highway", "steps"), false},
		{"under construction", tags("highway", "construction"), false},
		{"not a highway at all", tags("building", "yes"), false},

		// --- explicit prohibition wins over everything ---
		{"road with bicycle=no", tags("highway", "primary", "bicycle", "no"), false},
		{"cycleway tagged bicycle=no", tags("highway", "cycleway", "bicycle", "no"), false},
		{"dismount required", tags("highway", "path", "bicycle", "dismount"), false},
		{"use_sidepath means ride the parallel track instead",
			tags("highway", "primary", "bicycle", "use_sidepath"), false},

		// --- service road subtypes ---
		{"alley is a legitimate connection", tags("highway", "service", "service", "alley"), true},
		{"untagged service road", tags("highway", "service"), true},
		{"parking aisle", tags("highway", "service", "service", "parking_aisle"), false},
		{"driveway", tags("highway", "service", "service", "driveway"), false},
		{"drive-through lane", tags("highway", "service", "service", "drive-through"), false},

		// --- access restrictions ---
		{"private road", tags("highway", "service", "access", "private"), false},
		{"private road with bike access carved out",
			tags("highway", "service", "access", "private", "bicycle", "yes"), true},

		// --- footways need explicit permission (US default is no bikes) ---
		{"plain footway", tags("highway", "footway"), false},
		{"footway signed for bikes", tags("highway", "footway", "bicycle", "yes"), true},
		{"footway designated for bikes", tags("highway", "footway", "bicycle", "designated"), true},
		{"pedestrian street without bike access", tags("highway", "pedestrian"), false},
		{"pedestrian street permitting bikes",
			tags("highway", "pedestrian", "bicycle", "permissive"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Routable(tc.tags); got != tc.want {
				t.Errorf("Routable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyInfra(t *testing.T) {
	cases := []struct {
		name string
		tags osm.Tags
		want InfraClass
	}{
		// A separately-drawn cycleway is by definition physically separate.
		{"dedicated cycleway", tags("highway", "cycleway"), InfraProtectedTrack},
		{"shared-use path", tags("highway", "path"), InfraPath},
		{"footway open to bikes", tags("highway", "footway", "bicycle", "yes"), InfraPedestrian},

		// --- on-street facilities ---
		{"bare residential street", tags("highway", "residential"), InfraNone},
		{"painted lane", tags("highway", "secondary", "cycleway", "lane"), InfraPaintedLane},
		{"painted lane on the right", tags("highway", "secondary", "cycleway:right", "lane"), InfraPaintedLane},
		{"lanes on both sides", tags("highway", "secondary", "cycleway:both", "lane"), InfraPaintedLane},
		{"raised track alongside a road", tags("highway", "primary", "cycleway", "track"), InfraProtectedTrack},
		{"sharrow", tags("highway", "residential", "cycleway", "shared_lane"), InfraSharedLane},
		{"bus lane shared with bikes", tags("highway", "primary", "cycleway", "share_busway"), InfraSharedLane},

		// A buffer upgrades paint, because lateral space is what actually
		// reduces stress and exposure.
		{"buffered lane", tags("highway", "secondary", "cycleway", "lane", "cycleway:buffer", "yes"), InfraBufferedLane},
		{"buffered lane, side-specific tagging",
			tags("highway", "secondary", "cycleway:right", "lane", "cycleway:right:buffer", "1.5"), InfraBufferedLane},
		{"lane with a physical separator",
			tags("highway", "secondary", "cycleway", "lane", "cycleway:separation", "flex_post"), InfraBufferedLane},

		// --- disagreement between sides: the best facility wins ---
		{"track one side, paint the other",
			tags("highway", "primary", "cycleway:left", "lane", "cycleway:right", "track"), InfraProtectedTrack},
		{"paint one side, nothing the other",
			tags("highway", "primary", "cycleway:left", "no", "cycleway:right", "lane"), InfraPaintedLane},

		// "separate" means the facility is mapped as its own way, which we
		// pick up there. Counting it here too would double-count it.
		{"facility mapped separately", tags("highway", "primary", "cycleway", "separate"), InfraNone},
		{"explicitly no facility", tags("highway", "primary", "cycleway", "no"), InfraNone},

		// Contraflow tagging still describes a real lane.
		{"contraflow lane", tags("highway", "residential", "cycleway", "opposite_lane"), InfraPaintedLane},
		{"contraflow track", tags("highway", "residential", "cycleway", "opposite_track"), InfraProtectedTrack},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyInfra(tc.tags); got != tc.want {
				t.Errorf("ClassifyInfra = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInfraRankIsTotalOrder guards the ranking the classifier relies on when
// two sides of a street disagree.
func TestInfraRankIsTotalOrder(t *testing.T) {
	order := []InfraClass{InfraNone, InfraSharedLane, InfraPaintedLane, InfraBufferedLane, InfraProtectedTrack}
	for i := 1; i < len(order); i++ {
		if rank[order[i]] <= rank[order[i-1]] {
			t.Errorf("rank[%s]=%d should exceed rank[%s]=%d",
				order[i], rank[order[i]], order[i-1], rank[order[i-1]])
		}
	}
}
