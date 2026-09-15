package osm

import (
	"testing"

	"github.com/paulmach/osm"
)

// tags builds an osm.Tags from pairs, for terse test cases.
func tags(kv ...string) osm.Tags {
	if len(kv)%2 != 0 {
		panic("tags: odd number of arguments")
	}
	var t osm.Tags
	for i := 0; i < len(kv); i += 2 {
		t = append(t, osm.Tag{Key: kv[i], Value: kv[i+1]})
	}
	return t
}

func TestParseMaxSpeedMPH(t *testing.T) {
	cases := []struct {
		in   string
		want *int16
	}{
		{"35 mph", i16(35)},
		{"35mph", i16(35)},
		{"MPH 35", i16(35)}, // unit before the number still parses
		// A bare number is km/h by OSM convention. Getting this wrong would
		// read a 50 km/h road as 50 mph and score it far too dangerous.
		{"50", i16(31)},
		{"50 km/h", i16(31)},
		{"30 mph;40 mph", i16(30)}, // first value wins
		{"", nil},
		{"none", nil},
		{"signals", nil},
		{"walk", nil},
		{"garbage", nil},
		{"999", nil}, // implausible, almost certainly a tagging error
		{"0", nil},
	}
	for _, tc := range cases {
		got := parseMaxSpeedMPH(tc.in)
		if !eqI16(got, tc.want) {
			t.Errorf("parseMaxSpeedMPH(%q) = %v, want %v", tc.in, show(got), show(tc.want))
		}
	}
}

func TestParseLanes(t *testing.T) {
	cases := []struct {
		in   string
		want *int16
	}{
		{"2", i16(2)},
		{"4", i16(4)},
		{"2;3", i16(2)},
		{"", nil},
		{"lots", nil},
		{"99", nil}, // beyond the CHECK constraint on the column
		{"-1", nil},
	}
	for _, tc := range cases {
		got := parseLanes(tc.in)
		if !eqI16(got, tc.want) {
			t.Errorf("parseLanes(%q) = %v, want %v", tc.in, show(got), show(tc.want))
		}
	}
}

func TestParseTriBool(t *testing.T) {
	if v := parseTriBool("yes"); v == nil || !*v {
		t.Error(`parseTriBool("yes") should be true`)
	}
	if v := parseTriBool("no"); v == nil || *v {
		t.Error(`parseTriBool("no") should be false`)
	}
	// The distinction the whole model depends on: unknown is not false.
	if v := parseTriBool(""); v != nil {
		t.Error(`parseTriBool("") should be nil (unknown), not false`)
	}
	if v := parseTriBool("maybe"); v != nil {
		t.Error(`parseTriBool("maybe") should be nil`)
	}
}

func TestDirections(t *testing.T) {
	cases := []struct {
		name     string
		tags     osm.Tags
		fwd, bwd bool
	}{
		{"plain two-way street", tags("highway", "residential"), true, true},
		{"one-way", tags("highway", "residential", "oneway", "yes"), true, false},
		{"reversed one-way", tags("highway", "residential", "oneway", "-1"), false, true},
		{"roundabout is implicitly one-way",
			tags("highway", "tertiary", "junction", "roundabout"), true, false},

		// The case that matters most for a safety-first router: a one-way
		// street that allows contraflow cycling is a two-way street for us,
		// and these are often the quietest available connections.
		{"one-way with contraflow cycling permitted",
			tags("highway", "residential", "oneway", "yes", "oneway:bicycle", "no"), true, true},
		{"legacy contraflow tagging",
			tags("highway", "residential", "oneway", "yes", "cycleway", "opposite_lane"), true, true},
		{"one-way for bikes specifically",
			tags("highway", "residential", "oneway", "no", "oneway:bicycle", "yes"), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fwd, bwd := directions(tc.tags)
			if fwd != tc.fwd || bwd != tc.bwd {
				t.Errorf("directions = (fwd=%v, bwd=%v), want (fwd=%v, bwd=%v)", fwd, bwd, tc.fwd, tc.bwd)
			}
		})
	}
}

func TestNodeControl(t *testing.T) {
	cases := []struct {
		name string
		tags osm.Tags
		want string
	}{
		{"signal", tags("highway", "traffic_signals"), "signal"},
		{"stop", tags("highway", "stop"), "stop"},
		{"yield", tags("highway", "give_way"), "yield"},
		{"plain crossing", tags("highway", "crossing"), "crossing"},
		{"signalised crossing", tags("crossing", "traffic_signals"), "signal"},
		{"untagged junction", tags(), "none"},
		{"unrelated tags", tags("barrier", "gate"), "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NodeControl(tc.tags); got != tc.want {
				t.Errorf("NodeControl = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseWayPopulatesAttributes(t *testing.T) {
	a := ParseWay(tags(
		"highway", "secondary",
		"name", "South Lamar Boulevard",
		"maxspeed", "40 mph",
		"lanes", "4",
		"surface", "asphalt",
		"lit", "yes",
		"cycleway:right", "lane",
	))

	if a.Highway != "secondary" {
		t.Errorf("Highway = %q", a.Highway)
	}
	if a.Name != "South Lamar Boulevard" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.MaxSpeedMPH == nil || *a.MaxSpeedMPH != 40 {
		t.Errorf("MaxSpeedMPH = %v", show(a.MaxSpeedMPH))
	}
	if a.Lanes == nil || *a.Lanes != 4 {
		t.Errorf("Lanes = %v", show(a.Lanes))
	}
	if a.Lit == nil || !*a.Lit {
		t.Error("Lit should be true")
	}
	if a.Infra != InfraPaintedLane {
		t.Errorf("Infra = %q, want %q", a.Infra, InfraPaintedLane)
	}
	if !a.Forward || !a.Backward {
		t.Error("should be traversable both ways")
	}
}

// --- small helpers -------------------------------------------------------

func i16(v int16) *int16 { return &v }

func eqI16(a, b *int16) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func show(v *int16) any {
	if v == nil {
		return "nil"
	}
	return *v
}
