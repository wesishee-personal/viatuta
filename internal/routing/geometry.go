package routing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
)

// FetchGeometry loads the shape of a computed path from Postgres.
//
// Geometry is kept out of the in-memory graph because the router never needs
// it — only the final answer does. A route is a few hundred edges, so this is
// one small indexed query at the end of a request rather than anything in the
// hot path.
//
// The second return value reports where each edge begins in the geometry, so
// a caller can attribute stretches of the line back to the roads they came
// from. See assemble for the indexing convention.
func FetchGeometry(ctx context.Context, pool *pgxpool.Pool, g *graph.Graph, p *Path) ([]geo.LatLon, []int32, error) {
	if len(p.Edges) == 0 {
		return nil, nil, nil
	}

	ids := make([]int64, len(p.Edges))
	for i, e := range p.Edges {
		ids[i] = g.DBID[e]
	}

	// One query for every edge, then reassembled in path order. Fetching
	// them one at a time would mean hundreds of round trips.
	rows, err := pool.Query(ctx,
		`SELECT id, ST_AsGeoJSON(geom) FROM routing_edge WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("routing: fetching geometry: %w", err)
	}
	defer rows.Close()

	shapes := make(map[int64][]geo.LatLon, len(ids))
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, nil, fmt.Errorf("routing: scanning geometry: %w", err)
		}
		pts, err := decodeLineString(raw)
		if err != nil {
			return nil, nil, err
		}
		shapes[id] = pts
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("routing: reading geometry: %w", err)
	}

	return assemble(ids, shapes)
}

// assemble concatenates per-edge shapes in path order and reports where each
// edge begins in the result.
//
// offs has len(ids)+1 entries, and edge i's polyline is
// pts[offs[i] : offs[i+1]+1]. The end index is INCLUSIVE because consecutive
// edges share a vertex: the last point of one edge is the first point of the
// next, stored once. A caller slicing these ranges gets polylines that join
// up rather than ones with visible gaps at every junction.
//
// This is split out from FetchGeometry so the index arithmetic can be tested
// without a database — an off-by-one here misattributes a stretch of road to
// the wrong edge, which is invisible until someone looks closely at a map.
func assemble(ids []int64, shapes map[int64][]geo.LatLon) ([]geo.LatLon, []int32, error) {
	out := make([]geo.LatLon, 0, len(ids)*4)
	offs := make([]int32, len(ids)+1)

	for i, id := range ids {
		pts, ok := shapes[id]
		if !ok {
			return nil, nil, fmt.Errorf("routing: edge %d has no geometry", id)
		}
		// The first point of each edge repeats the last point of the
		// previous one, since they share a junction. Emitting it twice
		// would produce a zero-length segment in the output.
		if len(out) > 0 && len(pts) > 0 {
			pts = pts[1:]
		}
		out = append(out, pts...)

		// An edge with no geometry of its own leaves offs[i+1] == offs[i],
		// a zero-length range the caller can skip rather than crash on.
		if len(out) == 0 {
			offs[i+1] = 0
			continue
		}
		offs[i+1] = int32(len(out) - 1)
	}
	return out, offs, nil
}

// geoJSONLine is the subset of GeoJSON that ST_AsGeoJSON emits for a
// LineString.
type geoJSONLine struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}

// decodeLineString parses PostGIS GeoJSON output.
//
// GeoJSON coordinates are [longitude, latitude]; LatLon stores latitude
// first. The swap happens here, explicitly, and nowhere else.
func decodeLineString(raw string) ([]geo.LatLon, error) {
	var l geoJSONLine
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return nil, fmt.Errorf("routing: decoding geometry: %w", err)
	}
	pts := make([]geo.LatLon, 0, len(l.Coordinates))
	for _, c := range l.Coordinates {
		if len(c) < 2 {
			return nil, fmt.Errorf("routing: malformed coordinate pair %v", c)
		}
		pts = append(pts, geo.LatLon{Lat: c[1], Lon: c[0]})
	}
	return pts, nil
}
