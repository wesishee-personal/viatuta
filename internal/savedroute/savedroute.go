// Package savedroute stores routes a rider has chosen to keep.
package savedroute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/safety"
)

// ErrNotFound covers both a missing route and one belonging to another user.
//
// They are deliberately the same error: replying "that exists but is not
// yours" would let anyone probe which route ids are real.
var ErrNotFound = errors.New("route not found")

// MaxNameLen bounds the user-supplied name.
const MaxNameLen = 120

// Route is a stored route.
type Route struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Waypoints   []geo.LatLon   `json:"waypoints"`
	DistanceM   float64        `json:"distance_m"`
	DurationS   *int           `json:"duration_s,omitempty"`
	SafetyScore *float64       `json:"safety_score,omitempty"`
	Profile     safety.Profile `json:"profile"`
	CreatedAt   time.Time      `json:"created_at"`

	// Geometry is populated only when a single route is fetched. Listing
	// omits it: a route is thousands of coordinates, and returning fifty of
	// them at once would make the list endpoint enormous for no benefit.
	Geometry []geo.LatLon `json:"geometry,omitempty"`
}

// Input is everything needed to store a route.
type Input struct {
	Name        string
	Waypoints   []geo.LatLon
	Geometry    []geo.LatLon
	DistanceM   float64
	DurationS   int
	SafetyScore float64
	Profile     safety.Profile
}

// ValidateName checks the user-supplied name.
func ValidateName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(n)) > MaxNameLen {
		return "", fmt.Errorf("name must be at most %d characters", MaxNameLen)
	}
	return n, nil
}

// Create stores a route for a user.
func Create(ctx context.Context, pool *pgxpool.Pool, userID string, in Input) (Route, error) {
	profileJSON, err := json.Marshal(in.Profile)
	if err != nil {
		return Route{}, fmt.Errorf("savedroute: encoding profile: %w", err)
	}

	// Geometry is passed as WKT and converted by PostGIS, which keeps the
	// driver free of any geometry encoding.
	wpWKT := multiPointWKT(in.Waypoints)
	geomWKT := lineStringWKT(in.Geometry)

	var r Route
	var profileRaw []byte
	err = pool.QueryRow(ctx, `
		INSERT INTO saved_route
			(user_id, name, waypoints, geom, distance_m, duration_s, safety_score, profile)
		VALUES ($1, $2,
		        ST_GeomFromText($3, 4326), ST_GeomFromText($4, 4326),
		        $5, $6, $7, $8)
		RETURNING id, name, distance_m, duration_s, safety_score, profile, created_at`,
		userID, in.Name, wpWKT, geomWKT,
		in.DistanceM, in.DurationS, in.SafetyScore, profileJSON).
		Scan(&r.ID, &r.Name, &r.DistanceM, &r.DurationS, &r.SafetyScore, &profileRaw, &r.CreatedAt)
	if err != nil {
		return Route{}, fmt.Errorf("savedroute: creating: %w", err)
	}

	_ = json.Unmarshal(profileRaw, &r.Profile)
	r.Waypoints = in.Waypoints
	return r, nil
}

// List returns a user's routes, newest first, without geometry.
func List(ctx context.Context, pool *pgxpool.Pool, userID string, limit int) ([]Route, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := pool.Query(ctx, `
		SELECT id, name, distance_m, duration_s, safety_score, profile, created_at,
		       ST_AsGeoJSON(waypoints)
		FROM saved_route
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("savedroute: listing: %w", err)
	}
	defer rows.Close()

	out := []Route{}
	for rows.Next() {
		var r Route
		var profileRaw []byte
		var wpJSON string
		if err := rows.Scan(&r.ID, &r.Name, &r.DistanceM, &r.DurationS, &r.SafetyScore,
			&profileRaw, &r.CreatedAt, &wpJSON); err != nil {
			return nil, fmt.Errorf("savedroute: scanning: %w", err)
		}
		_ = json.Unmarshal(profileRaw, &r.Profile)
		r.Waypoints = decodeMultiPoint(wpJSON)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one route including its geometry.
//
// The user id is part of the WHERE clause rather than checked afterwards, so
// there is no code path in which another user's route is loaded at all.
func Get(ctx context.Context, pool *pgxpool.Pool, userID, id string) (Route, error) {
	var r Route
	var profileRaw []byte
	var wpJSON, geomJSON string

	err := pool.QueryRow(ctx, `
		SELECT id, name, distance_m, duration_s, safety_score, profile, created_at,
		       ST_AsGeoJSON(waypoints), ST_AsGeoJSON(geom)
		FROM saved_route
		WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&r.ID, &r.Name, &r.DistanceM, &r.DurationS, &r.SafetyScore,
			&profileRaw, &r.CreatedAt, &wpJSON, &geomJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return Route{}, ErrNotFound
	}
	if err != nil {
		return Route{}, fmt.Errorf("savedroute: getting: %w", err)
	}

	_ = json.Unmarshal(profileRaw, &r.Profile)
	r.Waypoints = decodeMultiPoint(wpJSON)
	r.Geometry = decodeLineString(geomJSON)
	return r, nil
}

// Delete removes a route.
func Delete(ctx context.Context, pool *pgxpool.Pool, userID, id string) error {
	tag, err := pool.Exec(ctx, `DELETE FROM saved_route WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("savedroute: deleting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- geometry encoding ---------------------------------------------------

// WKT uses "longitude latitude" order, the opposite of LatLon's fields.

func multiPointWKT(pts []geo.LatLon) string {
	var b strings.Builder
	b.WriteString("MULTIPOINT(")
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "(%.7f %.7f)", p.Lon, p.Lat)
	}
	b.WriteByte(')')
	return b.String()
}

func lineStringWKT(pts []geo.LatLon) string {
	var b strings.Builder
	b.WriteString("LINESTRING(")
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.7f %.7f", p.Lon, p.Lat)
	}
	b.WriteByte(')')
	return b.String()
}

type geoJSONGeom struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

func decodeLineString(raw string) []geo.LatLon {
	var g geoJSONGeom
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil
	}
	var coords [][]float64
	if json.Unmarshal(g.Coordinates, &coords) != nil {
		return nil
	}
	return toLatLons(coords)
}

func decodeMultiPoint(raw string) []geo.LatLon {
	var g geoJSONGeom
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil
	}
	var coords [][]float64
	if json.Unmarshal(g.Coordinates, &coords) != nil {
		return nil
	}
	return toLatLons(coords)
}

func toLatLons(coords [][]float64) []geo.LatLon {
	out := make([]geo.LatLon, 0, len(coords))
	for _, c := range coords {
		if len(c) >= 2 {
			out = append(out, geo.LatLon{Lat: c[1], Lon: c[0]})
		}
	}
	return out
}
