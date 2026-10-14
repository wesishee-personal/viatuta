package hazard

import "testing"

func TestAngleDiff(t *testing.T) {
	cases := []struct{ a, b, want float64 }{
		{0, 0, 0},
		{90, 270, 180},
		{350, 10, 20}, // wraps through north
		{10, 350, 20},
		{0, 359, 1},
		{45, 135, 90},
		{180, 0, 180},
		{720, 10, 10},
	}
	for _, tc := range cases {
		if got := AngleDiff(tc.a, tc.b); got != tc.want {
			t.Errorf("AngleDiff(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func f(v float64) *float64 { return &v }

func TestSubmissionHeadingValidation(t *testing.T) {
	cases := []struct {
		name string
		h    *float64
		ok   bool
	}{
		{"absent is fine", nil, true},
		{"north", f(0), true},
		{"just under a full turn", f(359.9), true},
		{"a full turn", f(360), false},
		{"negative", f(-1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Submission{Location: validPoint(), Kind: "pothole", HeadingDeg: tc.h}
			if err := s.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestHeadingIsStoredAsWholeDegreesWrappingAtNorth(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{{0, 0}, {89.4, 89}, {89.6, 90}, {359.4, 359}, {359.6, 0}}
	for _, tc := range cases {
		got := Submission{HeadingDeg: f(tc.in)}.heading()
		if got == nil || *got != tc.want {
			t.Errorf("heading(%v) = %v, want %d", tc.in, got, tc.want)
		}
	}
	if got := (Submission{}).heading(); got != nil {
		t.Errorf("heading with none given = %v, want nil", *got)
	}
}

// TestDirectionalKindsMatchTheDesign pins which kinds let heading steer
// routing, so changing the set is deliberate.
func TestDirectionalKindsMatchTheDesign(t *testing.T) {
	want := map[string]bool{
		"accident": true, "poor_visibility": true, "aggressive_traffic": true,
		"pothole": true, "glass": true, "debris": true,
		"slippery_surface": true, "blocked_lane": true,
	}
	got := map[string]bool{}
	for _, k := range directionalKinds() {
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%s should be directional", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("%s should not be directional", k)
		}
	}
}
