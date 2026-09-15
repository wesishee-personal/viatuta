package httpapi

import (
	"testing"

	"github.com/wesishee/viatuta/internal/graph"
)

// segGraph builds the minimum Graph buildSegments reads: per-edge LTS,
// facility class, and length.
func segGraph(lts []uint8, infra []graph.Infra, length []float32) *graph.Graph {
	return &graph.Graph{LTS: lts, Infra: infra, LengthM: length}
}

func edgeIdx(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(i)
	}
	return out
}

func TestBuildSegmentsMergesLikeRuns(t *testing.T) {
	// Four edges: two LTS1 protected, then two LTS3 with no facility.
	g := segGraph(
		[]uint8{1, 1, 3, 3},
		[]graph.Infra{graph.InfraProtectedTrack, graph.InfraProtectedTrack, graph.InfraNone, graph.InfraNone},
		[]float32{100, 150, 200, 50},
	)
	offs := []int32{0, 2, 5, 9, 12}

	got := buildSegments(g, edgeIdx(4), offs)
	if len(got) != 2 {
		t.Fatalf("got %d segments, want 2 merged runs: %+v", len(got), got)
	}

	if got[0].LTS != 1 || got[0].Infra != "protected_track" {
		t.Errorf("segment 0 = LTS %d %q, want LTS 1 protected_track", got[0].LTS, got[0].Infra)
	}
	if got[0].Start != 0 || got[0].End != 5 {
		t.Errorf("segment 0 spans %d..%d, want 0..5", got[0].Start, got[0].End)
	}
	if got[0].LengthM != 250 {
		t.Errorf("segment 0 length = %v, want 250 (100+150)", got[0].LengthM)
	}
	if got[1].Start != 5 || got[1].End != 12 {
		t.Errorf("segment 1 spans %d..%d, want 5..12", got[1].Start, got[1].End)
	}
}

// A change in EITHER field starts a new run — two roads can share a stress
// level while feeling completely different to ride.
func TestBuildSegmentsSplitsOnEitherField(t *testing.T) {
	cases := []struct {
		name  string
		lts   []uint8
		infra []graph.Infra
	}{
		{
			"stress changes",
			[]uint8{2, 3},
			[]graph.Infra{graph.InfraPaintedLane, graph.InfraPaintedLane},
		},
		{
			"facility changes",
			[]uint8{2, 2},
			[]graph.Infra{graph.InfraPaintedLane, graph.InfraBufferedLane},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := segGraph(tc.lts, tc.infra, []float32{100, 100})
			got := buildSegments(g, edgeIdx(2), []int32{0, 3, 6})
			if len(got) != 2 {
				t.Fatalf("got %d segments, want 2: %+v", len(got), got)
			}
		})
	}
}

// Segments must tile the line with no gaps and no overlaps: each one ends
// exactly where the next begins, because they share that junction vertex.
func TestBuildSegmentsTileTheLine(t *testing.T) {
	g := segGraph(
		[]uint8{1, 3, 1, 4},
		[]graph.Infra{graph.InfraPath, graph.InfraNone, graph.InfraPath, graph.InfraNone},
		[]float32{100, 200, 300, 400},
	)
	offs := []int32{0, 4, 7, 11, 14}

	got := buildSegments(g, edgeIdx(4), offs)
	if len(got) != 4 {
		t.Fatalf("got %d segments, want 4 (nothing merges here)", len(got))
	}

	if got[0].Start != 0 {
		t.Errorf("first segment starts at %d, want 0", got[0].Start)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Start != got[i-1].End {
			t.Errorf("gap between segment %d (ends %d) and %d (starts %d)",
				i-1, got[i-1].End, i, got[i].Start)
		}
	}
	if last := got[len(got)-1].End; last != int(offs[len(offs)-1]) {
		t.Errorf("last segment ends at %d, want %d (the final coordinate)", last, offs[len(offs)-1])
	}

	var total float64
	for _, s := range got {
		total += s.LengthM
	}
	if total != 1000 {
		t.Errorf("lengths sum to %v, want 1000 — segments must account for the whole route", total)
	}
}

// An edge with no geometry of its own still has length. Folding it into the
// neighbouring run keeps the distances honest without emitting a segment that
// draws nothing.
func TestBuildSegmentsFoldsDegenerateEdge(t *testing.T) {
	g := segGraph(
		[]uint8{1, 2, 1},
		[]graph.Infra{graph.InfraPath, graph.InfraNone, graph.InfraPath},
		[]float32{100, 25, 100},
	)
	// The middle edge contributed no coordinates: offs[1] == offs[2].
	offs := []int32{0, 3, 3, 7}

	got := buildSegments(g, edgeIdx(3), offs)

	var total float64
	for _, s := range got {
		total += s.LengthM
		if s.End <= s.Start {
			t.Errorf("segment %+v draws nothing", s)
		}
	}
	if total != 225 {
		t.Errorf("lengths sum to %v, want 225 — the degenerate edge's length was lost", total)
	}
}

func TestBuildSegmentsRejectsMismatchedOffsets(t *testing.T) {
	g := segGraph([]uint8{1}, []graph.Infra{graph.InfraPath}, []float32{100})
	if got := buildSegments(g, edgeIdx(1), []int32{0}); got != nil {
		t.Errorf("want nil for offsets that do not match the edge count, got %+v", got)
	}
	if got := buildSegments(g, nil, nil); got != nil {
		t.Errorf("want nil for an empty path, got %+v", got)
	}
}
