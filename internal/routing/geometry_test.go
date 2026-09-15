package routing

import (
	"testing"

	"github.com/wesishee/viatuta/internal/geo"
)

// pt is a terse coordinate for table-driven tests; the values are arbitrary
// but distinct, so a misplaced vertex is visible in a failure message.
func pt(n float64) geo.LatLon { return geo.LatLon{Lat: 30 + n/1000, Lon: -97 - n/1000} }

// line builds an edge shape from a list of vertex numbers.
func line(ns ...float64) []geo.LatLon {
	out := make([]geo.LatLon, len(ns))
	for i, n := range ns {
		out[i] = pt(n)
	}
	return out
}

func TestAssembleDropsSharedJunctionVertex(t *testing.T) {
	// Two edges meeting at vertex 2: 0-1-2 then 2-3-4.
	ids := []int64{10, 11}
	shapes := map[int64][]geo.LatLon{
		10: line(0, 1, 2),
		11: line(2, 3, 4),
	}

	pts, offs, err := assemble(ids, shapes)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	// The junction appears once, not twice.
	if len(pts) != 5 {
		t.Fatalf("got %d points, want 5 (the shared vertex stored once)", len(pts))
	}
	for i, want := range line(0, 1, 2, 3, 4) {
		if pts[i] != want {
			t.Errorf("point %d = %v, want %v", i, pts[i], want)
		}
	}

	// ...but still bounds both edges.
	if got := []int32{0, 2, 4}; !equal(offs, got) {
		t.Errorf("offs = %v, want %v", offs, got)
	}
}

// TestAssembleRangesReconstructEachEdge is the property that matters: slicing
// [offs[i] : offs[i+1]+1] must give back exactly the edge that went in. An
// off-by-one here attributes a stretch of road to the wrong edge, which is
// invisible until someone looks closely at a map.
func TestAssembleRangesReconstructEachEdge(t *testing.T) {
	ids := []int64{10, 11, 12}
	shapes := map[int64][]geo.LatLon{
		10: line(0, 1, 2),
		11: line(2, 3),
		12: line(3, 4, 5, 6),
	}
	originals := [][]geo.LatLon{line(0, 1, 2), line(2, 3), line(3, 4, 5, 6)}

	pts, offs, err := assemble(ids, shapes)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	if len(offs) != len(ids)+1 {
		t.Fatalf("len(offs) = %d, want %d", len(offs), len(ids)+1)
	}
	if got := int(offs[len(offs)-1]); got != len(pts)-1 {
		t.Errorf("last offset = %d, want %d (the final coordinate)", got, len(pts)-1)
	}

	for i := range ids {
		got := pts[offs[i] : offs[i+1]+1]
		want := originals[i]
		if len(got) != len(want) {
			t.Errorf("edge %d: got %d points, want %d", i, len(got), len(want))
			continue
		}
		for j := range want {
			if got[j] != want[j] {
				t.Errorf("edge %d point %d = %v, want %v", i, j, got[j], want[j])
			}
		}
	}
}

func TestAssembleOffsetsAreMonotonic(t *testing.T) {
	ids := []int64{10, 11, 12}
	shapes := map[int64][]geo.LatLon{
		10: line(0, 1),
		11: line(1, 2, 3),
		12: line(3, 4),
	}
	_, offs, err := assemble(ids, shapes)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for i := 1; i < len(offs); i++ {
		if offs[i] < offs[i-1] {
			t.Fatalf("offs went backwards at %d: %v", i, offs)
		}
	}
}

// An edge with no geometry must not crash or produce a negative index.
func TestAssembleToleratesEmptyEdge(t *testing.T) {
	ids := []int64{10, 11}
	shapes := map[int64][]geo.LatLon{
		10: {},
		11: line(0, 1, 2),
	}
	pts, offs, err := assemble(ids, shapes)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(pts) != 3 {
		t.Errorf("got %d points, want 3", len(pts))
	}
	for i, o := range offs {
		if o < 0 {
			t.Errorf("offs[%d] = %d, must never be negative", i, o)
		}
	}
}

func TestAssembleReportsMissingGeometry(t *testing.T) {
	_, _, err := assemble([]int64{10, 99}, map[int64][]geo.LatLon{10: line(0, 1)})
	if err == nil {
		t.Fatal("want an error naming the edge with no geometry, got nil")
	}
}

func equal(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
