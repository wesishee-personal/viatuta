package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
)

// ErrNoNearbyRoad means the requested point is too far from anything
// rideable — in the middle of a lake, or outside the extract entirely.
var ErrNoNearbyRoad = errors.New("no rideable road near this location")

// MaxSnapDistanceM bounds how far a request may be moved to reach the
// network. Beyond this the answer would be misleading rather than helpful.
const MaxSnapDistanceM = 1000

// Snap is the result of attaching a coordinate to the routing graph.
type Snap struct {
	NodeID    int32
	DistanceM float64
	EdgeDBID  int64
	RoadName  string
}

// Snapper attaches user coordinates to graph nodes.
//
// This uses PostGIS rather than an in-memory index. It runs a couple of times
// per request rather than millions of times inside the search loop, and the
// GIST index already exists, so the round trip is not worth avoiding yet.
type Snapper struct {
	pool *pgxpool.Pool
	// mainComponent marks nodes in the largest connected component. Snapping
	// into a 3-node island would produce a request that cannot possibly
	// succeed, and the resulting error would look like a routing bug.
	mainComponent []bool
}

func NewSnapper(pool *pgxpool.Pool, g *Graph) *Snapper {
	return &Snapper{pool: pool, mainComponent: g.LargestComponent()}
}

// Snap finds the best graph node to start or end a route at.
//
// It examines several nearby edges rather than only the closest, so that a
// point beside a disconnected driveway still attaches to the real network.
func (s *Snapper) Snap(ctx context.Context, p geo.LatLon) (Snap, error) {
	if !p.Valid() {
		return Snap{}, fmt.Errorf("graph: invalid coordinate %v", p)
	}

	const q = `
		WITH target AS (SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326) AS pt)
		SELECT e.id,
		       e.from_node,
		       e.to_node,
		       ST_Distance(e.geom::geography, t.pt::geography) AS dist_m,
		       ST_LineLocatePoint(e.geom, t.pt) AS frac,
		       coalesce(e.name, '')
		FROM routing_edge e, target t
		ORDER BY e.geom <-> t.pt
		LIMIT 10`

	rows, err := s.pool.Query(ctx, q, p.Lon, p.Lat)
	if err != nil {
		return Snap{}, fmt.Errorf("graph: snapping: %w", err)
	}
	defer rows.Close()

	var fallback Snap
	var haveFallback bool

	for rows.Next() {
		var (
			edgeID      int64
			from, to    int64
			distM, frac float64
			name        string
		)
		if err := rows.Scan(&edgeID, &from, &to, &distM, &frac, &name); err != nil {
			return Snap{}, fmt.Errorf("graph: scanning snap: %w", err)
		}
		if distM > MaxSnapDistanceM {
			break // ordered by distance, so everything after is worse
		}

		// Attach to whichever end of the edge the point is nearer along.
		node := int32(from)
		if frac >= 0.5 {
			node = int32(to)
		}

		cand := Snap{NodeID: node, DistanceM: distM, EdgeDBID: edgeID, RoadName: name}
		if !haveFallback {
			fallback, haveFallback = cand, true
		}
		if s.mainComponent == nil || s.mainComponent[node] {
			return cand, nil
		}
	}
	if err := rows.Err(); err != nil {
		return Snap{}, fmt.Errorf("graph: reading snap: %w", err)
	}

	// Everything nearby was in an island. Return the closest anyway and let
	// the router report honestly that no path exists.
	if haveFallback {
		return fallback, nil
	}
	return Snap{}, ErrNoNearbyRoad
}
