package osm

import (
	"log/slog"

	"github.com/wesishee/viatuta/internal/geo"
)

// Edge is a directed, routable segment ready to be written to Postgres.
type Edge struct {
	ID       int64 // dense, assigned here; becomes an array index in Phase 3
	WayID    int64
	FromNode int64 // dense routing_node id
	ToNode   int64
	Shape    []geo.LatLon // full geometry including both endpoints
	LengthM  float64
	Attrs    WayAttrs

	// Compass bearings at each end, in the direction of travel. Turn costs
	// compare the end bearing of one edge with the start bearing of the
	// next; see internal/safety/turn.go.
	BearingStart float32
	BearingEnd   float32
}

// Node is a junction ready to be written to Postgres.
type Node struct {
	ID      int64 // dense, assigned here
	OSMID   int64
	Loc     geo.LatLon
	Control string
}

// Graph is the complete result of the ingest.
type Graph struct {
	Nodes []Node
	Edges []Edge

	// Diagnostics, reported after ingest so a bad extract is obvious.
	SkippedMissingCoords int
	SkippedDegenerate    int
}

// Build turns scanned ways and node coordinates into a directed graph.
//
// Two things happen here. First, each way is cut at its junctions: the nodes
// in between merely trace the shape of the road and become geometry rather
// than graph nodes, which shrinks the graph by roughly an order of magnitude.
// Second, each resulting segment becomes one or two directed edges, because
// safety is not symmetric — grade, contraflow lanes, and one-way pairs all
// differ by direction.
func Build(res *ScanResult, nodes map[int64]NodeData, logger *slog.Logger) *Graph {
	g := &Graph{}

	// Dense ids are assigned lazily, so only junctions that survive into a
	// real edge consume one.
	denseID := make(map[int64]int64, len(res.NodeUse)/8)

	intern := func(osmID int64) (int64, bool) {
		if id, ok := denseID[osmID]; ok {
			return id, true
		}
		d, ok := nodes[osmID]
		if !ok {
			return 0, false // node lies outside the extract's bounding box
		}
		id := int64(len(g.Nodes))
		denseID[osmID] = id
		g.Nodes = append(g.Nodes, Node{
			ID:      id,
			OSMID:   osmID,
			Loc:     d.Loc,
			Control: control(d.Control),
		})
		return id, true
	}

	for _, w := range res.Ways {
		// Walk the way, accumulating shape points, and cut at every split
		// point after the first.
		start := 0
		for i := 1; i < len(w.Nodes); i++ {
			isLast := i == len(w.Nodes)-1
			if !res.IsSplitPoint(w.Nodes[i]) && !isLast {
				continue
			}

			g.addSegment(w, w.Nodes[start:i+1], nodes, intern)
			start = i
		}
	}

	logger.Info("built graph",
		"nodes", len(g.Nodes),
		"directed_edges", len(g.Edges),
		"skipped_missing_coords", g.SkippedMissingCoords,
		"skipped_degenerate", g.SkippedDegenerate,
	)
	return g
}

// addSegment converts one run of node ids into directed edges.
func (g *Graph) addSegment(w WayRecord, ids []int64, nodes map[int64]NodeData,
	intern func(int64) (int64, bool)) {

	if len(ids) < 2 {
		return
	}

	// A closed loop between the same two split points (a cul-de-sac circle)
	// cannot be represented as an edge with distinct endpoints.
	if ids[0] == ids[len(ids)-1] {
		g.SkippedDegenerate++
		return
	}

	shape := make([]geo.LatLon, 0, len(ids))
	for _, id := range ids {
		d, ok := nodes[id]
		if !ok {
			// A way crossing the extract boundary references nodes that
			// were clipped out. Dropping the segment is correct: we cannot
			// know where it goes.
			g.SkippedMissingCoords++
			return
		}
		shape = append(shape, d.Loc)
	}

	length := 0.0
	for i := 1; i < len(shape); i++ {
		length += geo.DistanceM(shape[i-1], shape[i])
	}
	// The length_m CHECK constraint requires a positive length, and a
	// zero-length edge would make cost-per-meter meaningless anyway.
	if length <= 0 {
		g.SkippedDegenerate++
		return
	}

	from, okFrom := intern(ids[0])
	to, okTo := intern(ids[len(ids)-1])
	if !okFrom || !okTo {
		g.SkippedMissingCoords++
		return
	}

	if w.Attrs.Forward {
		start, end := endBearings(shape)
		g.Edges = append(g.Edges, Edge{
			ID:           int64(len(g.Edges)),
			WayID:        w.ID,
			FromNode:     from,
			ToNode:       to,
			Shape:        shape,
			LengthM:      length,
			Attrs:        w.Attrs,
			BearingStart: start,
			BearingEnd:   end,
		})
	}
	if w.Attrs.Backward {
		rev := reverse(shape)
		start, end := endBearings(rev)
		g.Edges = append(g.Edges, Edge{
			ID:           int64(len(g.Edges)),
			WayID:        w.ID,
			FromNode:     to,
			ToNode:       from,
			Shape:        rev,
			LengthM:      length,
			Attrs:        w.Attrs,
			BearingStart: start,
			BearingEnd:   end,
		})
	}
}

// endBearings returns the compass bearing where the edge leaves its first
// node and where it arrives at its last.
//
// Adjacent shape points can be duplicated in OSM data, and a zero-length pair
// has no defined bearing, so the scan skips forward until the points differ.
func endBearings(shape []geo.LatLon) (start, end float32) {
	for i := 1; i < len(shape); i++ {
		if shape[i] != shape[0] {
			start = float32(geo.BearingDeg(shape[0], shape[i]))
			break
		}
	}
	last := len(shape) - 1
	for i := last - 1; i >= 0; i-- {
		if shape[i] != shape[last] {
			end = float32(geo.BearingDeg(shape[i], shape[last]))
			break
		}
	}
	return start, end
}

// reverse returns a reversed copy, leaving the original untouched because the
// forward edge still refers to it.
func reverse(in []geo.LatLon) []geo.LatLon {
	out := make([]geo.LatLon, len(in))
	for i, p := range in {
		out[len(in)-1-i] = p
	}
	return out
}

// control falls back to the column default when a node carries no recognised
// control tag, keeping the value inside the CHECK constraint.
func control(c string) string {
	if c == "" {
		return "none"
	}
	return c
}
