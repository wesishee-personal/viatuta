package geo

import (
	"math"
	"testing"
)

func closeTo(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (±%v)", what, got, want, tol)
	}
}

func TestDistanceM(t *testing.T) {
	// One degree of latitude is a fixed arc length on a sphere:
	// R * pi/180 = 111194.9 m. This is the cleanest possible check that
	// the formula and the radius constant agree.
	closeTo(t, DistanceM(LatLon{0, 0}, LatLon{1, 0}), 111194.9, 1.0, "1° latitude")

	// Same point must be exactly zero, not a tiny float artifact — the A*
	// heuristic relies on h(goal) == 0.
	if d := DistanceM(LatLon{30.2672, -97.7431}, LatLon{30.2672, -97.7431}); d != 0 {
		t.Errorf("distance to self = %v, want 0", d)
	}

	// Austin: the Capitol to Zilker Park is a little over 2 km.
	capitol := LatLon{30.2747, -97.7404}
	zilker := LatLon{30.2669, -97.7729}
	closeTo(t, DistanceM(capitol, zilker), 3250, 250, "Capitol→Zilker")

	// Distance is symmetric.
	if a, b := DistanceM(capitol, zilker), DistanceM(zilker, capitol); a != b {
		t.Errorf("not symmetric: %v vs %v", a, b)
	}
}

func TestBearingDeg(t *testing.T) {
	origin := LatLon{0, 0}
	closeTo(t, BearingDeg(origin, LatLon{1, 0}), 0, 0.001, "due north")
	closeTo(t, BearingDeg(origin, LatLon{0, 1}), 90, 0.001, "due east")
	closeTo(t, BearingDeg(origin, LatLon{0, -1}), 270, 0.001, "due west")

	// Bearing must always be in [0, 360).
	b := BearingDeg(LatLon{30.27, -97.74}, LatLon{30.26, -97.75})
	if b < 0 || b >= 360 {
		t.Errorf("bearing %v out of range [0,360)", b)
	}
}

func TestTurnAngleDeg(t *testing.T) {
	cases := []struct {
		name     string
		from, to float64
		want     float64
	}{
		{"straight on", 90, 90, 0},
		{"right turn", 0, 90, 90},
		{"left turn", 0, 270, -90},
		{"slight left across north", 10, 350, -20},
		{"slight right across north", 350, 10, 20},
		{"u-turn is reported at the negative bound", 0, 180, -180},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			closeTo(t, TurnAngleDeg(tc.from, tc.to), tc.want, 0.001, "turn angle")
		})
	}
}
