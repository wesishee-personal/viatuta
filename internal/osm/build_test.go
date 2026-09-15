package osm

import (
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/wesishee/viatuta/internal/geo"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// scanFixture builds a ScanResult the way ScanWays would: counting node
// references and forcing every way's endpoints to be split points.
func scanFixture(ways ...WayRecord) *ScanResult {
	res := &ScanResult{NodeUse: map[int64]uint8{}}
	for _, w := range ways {
		for _, id := range w.Nodes {
			if res.NodeUse[id] < 2 {
				res.NodeUse[id]++
			}
		}
		res.NodeUse[w.Nodes[0]] = 2
		res.NodeUse[w.Nodes[len(w.Nodes)-1]] = 2
		_ = w
	}
	res.Ways = ways
	return res
}

// nodesAt lays out node ids along a line of latitude, spaced by lon.
func nodesAt(ids ...int64) map[int64]NodeData {
	out := map[int64]NodeData{}
	for i, id := range ids {
		out[id] = NodeData{Loc: geo.LatLon{Lat: 30.27, Lon: -97.74 + float64(i)*0.001}}
	}
	return out
}

func twoWay() WayAttrs {
	return WayAttrs{Highway: "residential", Infra: InfraNone, Forward: true, Backward: true}
}

func TestBuildFoldsShapeNodesIntoGeometry(t *testing.T) {
	// A -- B -- C where B is referenced by only this way, so it describes
	// the road's shape rather than a junction.
	ways := []WayRecord{{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11, 12}}}
	res := scanFixture(ways...)

	g := Build(res, nodesAt(10, 11, 12), quietLogger())

	// Only the two endpoints become graph nodes.
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (shape node should not be a graph node)", len(g.Nodes))
	}
	// One segment, two directions.
	if len(g.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(g.Edges))
	}
	// But the shape node must survive in the geometry.
	if got := len(g.Edges[0].Shape); got != 3 {
		t.Errorf("shape points = %d, want 3 (B must remain in the geometry)", got)
	}
}

func TestBuildSplitsAtJunctions(t *testing.T) {
	// Two ways meeting at node 11 make it a junction, so the first way is
	// cut into two segments.
	ways := []WayRecord{
		{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11, 12}},
		{ID: 2, Attrs: twoWay(), Nodes: []int64{11, 20}},
	}
	res := scanFixture(ways...)

	g := Build(res, nodesAt(10, 11, 12, 20), quietLogger())

	if len(g.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(g.Nodes))
	}
	// Way 1 splits into A-B and B-C, way 2 is one segment: 3 segments,
	// each bidirectional.
	if len(g.Edges) != 6 {
		t.Fatalf("edges = %d, want 6", len(g.Edges))
	}
}

func TestBuildRespectsOneway(t *testing.T) {
	attrs := twoWay()
	attrs.Backward = false
	ways := []WayRecord{{ID: 1, Attrs: attrs, Nodes: []int64{10, 11}}}

	g := Build(scanFixture(ways...), nodesAt(10, 11), quietLogger())

	if len(g.Edges) != 1 {
		t.Fatalf("edges = %d, want 1 for a one-way segment", len(g.Edges))
	}
	if g.Edges[0].FromNode == g.Edges[0].ToNode {
		t.Error("edge endpoints should differ")
	}
}

func TestBuildReversesGeometryForBackwardEdge(t *testing.T) {
	ways := []WayRecord{{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11, 12}}}
	g := Build(scanFixture(ways...), nodesAt(10, 11, 12), quietLogger())

	fwd, bwd := g.Edges[0], g.Edges[1]

	if fwd.FromNode != bwd.ToNode || fwd.ToNode != bwd.FromNode {
		t.Error("backward edge should swap the endpoints")
	}
	// Geometry must run in the direction of travel, or Phase 4's bearings
	// and turn angles would be computed backwards.
	if fwd.Shape[0] != bwd.Shape[len(bwd.Shape)-1] {
		t.Error("backward geometry should be the forward geometry reversed")
	}
	// The forward edge's geometry must not have been mutated in place.
	if fwd.Shape[0] == fwd.Shape[len(fwd.Shape)-1] {
		t.Error("forward shape was corrupted by the reversal")
	}
	if math.Abs(fwd.LengthM-bwd.LengthM) > 1e-9 {
		t.Error("both directions should have the same length")
	}
}

func TestBuildComputesLength(t *testing.T) {
	ways := []WayRecord{{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11}}}
	g := Build(scanFixture(ways...), nodesAt(10, 11), quietLogger())

	// 0.001 degrees of longitude at latitude 30.27 is about 96 m.
	if l := g.Edges[0].LengthM; l < 90 || l > 100 {
		t.Errorf("length = %.1f m, want roughly 96 m", l)
	}
}

func TestBuildSkipsSegmentsWithMissingCoordinates(t *testing.T) {
	// Node 12 lies outside the extract, so its coordinates were clipped.
	ways := []WayRecord{{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11, 12}}}
	g := Build(scanFixture(ways...), nodesAt(10, 11), quietLogger())

	if len(g.Edges) != 0 {
		t.Errorf("edges = %d, want 0 when a coordinate is missing", len(g.Edges))
	}
	if g.SkippedMissingCoords == 0 {
		t.Error("the skip should be counted, not silently ignored")
	}
}

func TestBuildSkipsClosedLoops(t *testing.T) {
	// A cul-de-sac circle returning to its own start has no distinct
	// endpoints and cannot be one edge.
	ways := []WayRecord{{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11, 12, 10}}}
	res := scanFixture(ways...)
	g := Build(res, nodesAt(10, 11, 12), quietLogger())

	if g.SkippedDegenerate == 0 {
		t.Error("closed loop should be counted as degenerate")
	}
}

func TestBuildAssignsDenseIDs(t *testing.T) {
	ways := []WayRecord{
		{ID: 1, Attrs: twoWay(), Nodes: []int64{10, 11}},
		{ID: 2, Attrs: twoWay(), Nodes: []int64{11, 12}},
	}
	g := Build(scanFixture(ways...), nodesAt(10, 11, 12), quietLogger())

	// Phase 3 indexes arrays by these ids, so they must be exactly 0..n-1.
	for i, n := range g.Nodes {
		if n.ID != int64(i) {
			t.Fatalf("node %d has id %d; ids must be dense and ordered", i, n.ID)
		}
	}
	for i, e := range g.Edges {
		if e.ID != int64(i) {
			t.Fatalf("edge %d has id %d; ids must be dense and ordered", i, e.ID)
		}
		if e.FromNode < 0 || e.FromNode >= int64(len(g.Nodes)) {
			t.Fatalf("edge %d references node %d outside 0..%d", i, e.FromNode, len(g.Nodes)-1)
		}
	}
}
