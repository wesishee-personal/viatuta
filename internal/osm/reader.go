package osm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"github.com/wesishee/viatuta/internal/geo"
)

// WayRecord is a bike-routable way kept from the extract.
type WayRecord struct {
	ID    int64
	Attrs WayAttrs
	Nodes []int64 // OSM node ids, in order along the way
}

// ScanResult is the output of the first pass over the extract.
type ScanResult struct {
	Ways []WayRecord

	// NodeUse counts how many kept ways touch each node, saturating at 2.
	// A node used by two or more ways is a junction; way endpoints are
	// forced to 2 so that dead ends still become graph nodes.
	//
	// Only the distinction "1 versus 2 or more" matters, so a uint8 keeps
	// this map — the largest allocation in the ingest — as small as possible.
	NodeUse map[int64]uint8
}

// IsSplitPoint reports whether a node should become a graph node, as opposed
// to being folded into an edge's shape.
func (r *ScanResult) IsSplitPoint(id int64) bool { return r.NodeUse[id] >= 2 }

// ScanWays performs the first pass: read every way, keep the routable ones,
// and record which nodes they touch.
//
// Why two passes at all? A PBF file stores nodes before ways, but we cannot
// know which node coordinates matter until we have seen the ways that use
// them. Buffering every node in Austin would cost several gigabytes; reading
// ways first and then fetching only the ~10% of nodes we actually need costs
// a second read of a 65 MB file, which is far cheaper.
func ScanWays(ctx context.Context, path string, logger *slog.Logger) (*ScanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osm: opening extract: %w", err)
	}
	defer f.Close()

	scanner := osmpbf.New(ctx, f, runtime.GOMAXPROCS(0))
	defer scanner.Close()

	// Nodes and relations are skipped at the protobuf level, so they are
	// never decoded into Go values at all.
	scanner.SkipNodes = true
	scanner.SkipRelations = true
	scanner.FilterWay = func(w *osm.Way) bool { return Routable(w.Tags) }

	res := &ScanResult{NodeUse: make(map[int64]uint8, 1<<20)}
	var seen int

	for scanner.Scan() {
		w, ok := scanner.Object().(*osm.Way)
		if !ok {
			continue
		}
		seen++

		// A way needs at least two nodes to describe a segment.
		if len(w.Nodes) < 2 {
			continue
		}

		nodes := make([]int64, 0, len(w.Nodes))
		for _, wn := range w.Nodes {
			id := int64(wn.ID)
			nodes = append(nodes, id)
			if res.NodeUse[id] < 2 {
				res.NodeUse[id]++
			}
		}

		// Endpoints always become graph nodes, even on a dead-end street
		// that no other way touches.
		res.NodeUse[nodes[0]] = 2
		res.NodeUse[nodes[len(nodes)-1]] = 2

		res.Ways = append(res.Ways, WayRecord{
			ID:    int64(w.ID),
			Attrs: ParseWay(w.Tags),
			Nodes: nodes,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("osm: scanning ways: %w", err)
	}

	logger.Info("scanned ways",
		"routable_ways", len(res.Ways),
		"referenced_nodes", len(res.NodeUse),
	)
	return res, nil
}

// NodeData is the second pass's output for a single node.
type NodeData struct {
	Loc     geo.LatLon
	Control string
}

// ScanNodes performs the second pass: collect coordinates for the nodes the
// kept ways reference, plus traffic control for the ones that are junctions.
func ScanNodes(ctx context.Context, path string, res *ScanResult, logger *slog.Logger) (map[int64]NodeData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osm: opening extract: %w", err)
	}
	defer f.Close()

	scanner := osmpbf.New(ctx, f, runtime.GOMAXPROCS(0))
	defer scanner.Close()

	scanner.SkipWays = true
	scanner.SkipRelations = true
	scanner.FilterNode = func(n *osm.Node) bool {
		_, want := res.NodeUse[int64(n.ID)]
		return want
	}

	out := make(map[int64]NodeData, len(res.NodeUse))
	for scanner.Scan() {
		n, ok := scanner.Object().(*osm.Node)
		if !ok {
			continue
		}
		id := int64(n.ID)

		d := NodeData{Loc: geo.LatLon{Lat: n.Lat, Lon: n.Lon}}
		// Traffic control only matters where a rider actually makes a
		// decision, so it is read for junctions and skipped for the shape
		// nodes that merely trace a curve.
		if res.IsSplitPoint(id) {
			d.Control = NodeControl(n.Tags)
		}
		out[id] = d
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("osm: scanning nodes: %w", err)
	}

	logger.Info("scanned nodes", "resolved", len(out), "wanted", len(res.NodeUse))
	return out, nil
}
