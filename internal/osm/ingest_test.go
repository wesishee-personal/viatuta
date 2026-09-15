package osm

import (
	"strings"
	"testing"

	"github.com/wesishee/viatuta/internal/geo"
)

func TestCopyRowFormat(t *testing.T) {
	var r copyRow
	r.reset()
	r.Int(7)
	r.Str("Guadalupe Street")
	r.StrOrNull("")
	r.Float(123.456789, 3)
	r.OptInt16(i16(35))
	r.OptInt16(nil)
	r.OptBool(boolp(true))
	r.OptBool(boolp(false))
	r.OptBool(nil)

	got := string(r.finish())
	want := "7\tGuadalupe Street\t\\N\t123.457\t35\t\\N\tt\tf\t\\N\n"
	if got != want {
		t.Errorf("row =\n  %q\nwant\n  %q", got, want)
	}
}

// TestCopyRowEscaping is the test that matters most here. An unescaped tab
// inside a street name would shift every subsequent column by one, so a road
// would silently acquire another road's speed limit and bike lane.
func TestCopyRowEscaping(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tab", "a\tb", `a\tb`},
		{"newline", "a\nb", `a\nb`},
		{"carriage return", "a\rb", `a\rb`},
		{"backslash", `a\b`, `a\\b`},
		// A literal \N in the data must not be mistaken for NULL.
		{"literal backslash-N", `\N`, `\\N`},
		{"plain text is untouched", "South 1st Street", "South 1st Street"},
		{"unicode survives", "Café Bräuer", "Café Bräuer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r copyRow
			r.reset()
			r.Str(tc.in)
			got := strings.TrimSuffix(string(r.finish()), "\n")
			if got != tc.want {
				t.Errorf("escaped %q to %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEWKTEncoding(t *testing.T) {
	// PostGIS expects longitude first — the opposite of LatLon's field
	// order. Getting this backwards puts all of Austin in Antarctica.
	p := geo.LatLon{Lat: 30.2672, Lon: -97.7431}
	got := pointEWKT(p)
	want := "SRID=4326;POINT(-97.7431000 30.2672000)"
	if got != want {
		t.Errorf("pointEWKT = %q, want %q", got, want)
	}

	line := lineEWKT([]geo.LatLon{
		{Lat: 30.2672, Lon: -97.7431},
		{Lat: 30.2680, Lon: -97.7440},
	})
	wantLine := "SRID=4326;LINESTRING(-97.7431000 30.2672000,-97.7440000 30.2680000)"
	if line != wantLine {
		t.Errorf("lineEWKT = %q, want %q", line, wantLine)
	}
}

func boolp(b bool) *bool { return &b }
