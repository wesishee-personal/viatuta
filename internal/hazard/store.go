package hazard

import (
	"context"
	"encoding/json"
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
	if _, ok := Lookup(s.Kind); !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, s.Kind)
	}
	if len(s.Notes) > 500 {
		return fmt.Errorf("notes must be at most 500 characters")
	}
	return nil
}

// Create stores a hazard report and returns it.
//
// userID is the reporting account. The column stays nullable only because
// its foreign key is ON DELETE SET NULL: deleting an account keeps the safety
// data but drops the author.
func Create(ctx context.Context, pool *pgxpool.Pool, s Submission, userID string) (Report, error) {
	kind, ok := Lookup(s.Kind)
	if !ok {
		return Report{}, fmt.Errorf("%w: %q", ErrUnknownKind, s.Kind)
	}

	var r Report
	// created_at and expires_at both come from now(), which is fixed for the
	// statement, so the lifetime is exactly the kind's TTL.
	err := pool.QueryRow(ctx, `
		INSERT INTO hazard_report (user_id, geom, kind, notes, expires_at)
		VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4, NULLIF($5, ''),
		        now() + make_interval(secs => $6))
		RETURNING id, ST_Y(geom), ST_X(geom), kind, coalesce(notes, ''),
		          confirmations, created_at, expires_at`,
		userID, s.Location.Lon, s.Location.Lat, s.Kind, s.Notes, kind.TTL.Seconds()).
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
//
// Each confirmation extends the hazard's life by its kind's ConfirmExtension,
// but never past created_at + MaxLife.
func Confirm(ctx context.Context, pool *pgxpool.Pool, id int64) (Report, error) {
	var r Report
	err := pool.QueryRow(ctx, `
		UPDATE hazard_report
		SET confirmations = confirmations + 1,
		    expires_at = LEAST(
		        expires_at + make_interval(secs => coalesce(($2::jsonb -> kind ->> 'extend_s')::double precision, 0)),
		        created_at + make_interval(secs => coalesce(($2::jsonb -> kind ->> 'max_s')::double precision, 0)))
		WHERE id = $1 AND expires_at > now()
		RETURNING id, ST_Y(geom), ST_X(geom), kind, coalesce(notes, ''),
		          confirmations, created_at, expires_at`, id, lifetimesJSON()).
		Scan(&r.ID, &r.Location.Lat, &r.Location.Lon, &r.Kind, &r.Notes,
			&r.Confirmations, &r.CreatedAt, &r.ExpiresAt)
	if err != nil {
		return Report{}, fmt.Errorf("hazard: confirming report %d: %w", id, err)
	}
	return r, nil
}

// lifetimesJSON renders each kind's confirmation extension and maximum life
// for the Confirm query, so the numbers live in Go beside their explanation
// and are not duplicated as literals inside SQL.
func lifetimesJSON() string {
	type lifetime struct {
		ExtendS float64 `json:"extend_s"`
		MaxS    float64 `json:"max_s"`
	}
	m := make(map[string]lifetime, len(kinds))
	for _, k := range kinds {
		m[k.Name] = lifetime{ExtendS: k.ConfirmExtension.Seconds(), MaxS: k.MaxLife.Seconds()}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}" // unreachable for a map of plain structs
	}
	return string(b)
}
