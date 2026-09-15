package hazard

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
)

// ErrUnknownKind is returned for a hazard kind the schema does not accept.
var ErrUnknownKind = errors.New("unknown hazard kind")

// Submission is a new hazard report from a rider.
type Submission struct {
	Location geo.LatLon `json:"location"`
	Kind     string     `json:"kind"`
	Notes    string     `json:"notes,omitempty"`
}

// Validate checks a submission before it reaches the database.
func (s Submission) Validate() error {
	if !s.Location.Valid() {
		return fmt.Errorf("location is not a valid coordinate")
	}
	if _, ok := Kinds[s.Kind]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, s.Kind)
	}
	if len(s.Notes) > 500 {
		return fmt.Errorf("notes must be at most 500 characters")
	}
	return nil
}

// Create stores a hazard report and returns it.
//
// userID may be nil: reports are accepted anonymously for now, and the column
// is nullable to match. Requiring an account arrives with authentication.
func Create(ctx context.Context, pool *pgxpool.Pool, s Submission, userID *string) (Report, error) {
	var r Report
	err := pool.QueryRow(ctx, `
		INSERT INTO hazard_report (user_id, geom, kind, notes)
		VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4, NULLIF($5, ''))
		RETURNING id, ST_Y(geom), ST_X(geom), kind, coalesce(notes, ''),
		          confirmations, created_at, expires_at`,
		userID, s.Location.Lon, s.Location.Lat, s.Kind, s.Notes).
		Scan(&r.ID, &r.Location.Lat, &r.Location.Lon, &r.Kind, &r.Notes,
			&r.Confirmations, &r.CreatedAt, &r.ExpiresAt)
	if err != nil {
		return Report{}, fmt.Errorf("hazard: creating report: %w", err)
	}
	return r, nil
}

// ListInBBox returns active hazards inside a bounding box.
func ListInBBox(ctx context.Context, pool *pgxpool.Pool,
	minLon, minLat, maxLon, maxLat float64, limit int) ([]Report, error) {

	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := pool.Query(ctx, `
		SELECT id, ST_Y(geom), ST_X(geom), kind, coalesce(notes, ''),
		       confirmations, created_at, expires_at
		FROM hazard_report
		WHERE expires_at > now()
		  AND geom && ST_MakeEnvelope($1, $2, $3, $4, 4326)
		ORDER BY created_at DESC
		LIMIT $5`, minLon, minLat, maxLon, maxLat, limit)
	if err != nil {
		return nil, fmt.Errorf("hazard: listing reports: %w", err)
	}
	defer rows.Close()

	out := []Report{}
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.Location.Lat, &r.Location.Lon, &r.Kind,
			&r.Notes, &r.Confirmations, &r.CreatedAt, &r.ExpiresAt); err != nil {
			return nil, fmt.Errorf("hazard: scanning report: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Confirm records that another rider has seen the same hazard, which raises
// its weight and is the cheapest defence against a single bad report steering
// everyone's routes.
func Confirm(ctx context.Context, pool *pgxpool.Pool, id int64) (Report, error) {
	var r Report
	err := pool.QueryRow(ctx, `
		UPDATE hazard_report
		SET confirmations = confirmations + 1,
		    expires_at = expires_at + interval '14 days'
		WHERE id = $1 AND expires_at > now()
		RETURNING id, ST_Y(geom), ST_X(geom), kind, coalesce(notes, ''),
		          confirmations, created_at, expires_at`, id).
		Scan(&r.ID, &r.Location.Lat, &r.Location.Lon, &r.Kind, &r.Notes,
			&r.Confirmations, &r.CreatedAt, &r.ExpiresAt)
	if err != nil {
		return Report{}, fmt.Errorf("hazard: confirming report %d: %w", id, err)
	}
	return r, nil
}
