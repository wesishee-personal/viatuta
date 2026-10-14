package hazard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
)

var (
	// ErrUnknownKind is returned for a hazard kind the schema does not accept.
	ErrUnknownKind = errors.New("unknown hazard kind")

	// ErrNotFound means there is no ACTIVE hazard with that id: it never
	// existed, has expired, or was resolved.
	ErrNotFound = errors.New("no active hazard with that id")

	// ErrOwnReport is returned when a rider tries to confirm their own report.
	// Their report already counts as the first sighting.
	ErrOwnReport = errors.New("you reported this hazard")

	// ErrAlreadyVoted is returned when a rider has already confirmed or
	// resolved the hazard. Each rider gets one say.
	ErrAlreadyVoted = errors.New("you have already responded to this hazard")
)

// ResolveVotesNeeded is how many riders must say a hazard is gone before it
// is taken down. The original reporter can take down their own report alone.
const ResolveVotesNeeded = 2

// mergeLockKey serialises hazard creation; see Create.
const mergeLockKey = 7263001

const reportColumns = `id, ST_Y(geom), ST_X(geom), kind, coalesce(notes, ''),
	confirmations, created_at, expires_at`

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

// Create stores a hazard report, or merges it into an existing one.
//
// A report of the same kind within the kind's merge radius of an active hazard
// is the same hazard seen again, so it counts as a confirmation by this rider
// instead of adding a second marker. The bool result is true when a new hazard
// was created and false when the report was merged.
//
// userID is the reporting account. The column stays nullable only because
// its foreign key is ON DELETE SET NULL: deleting an account keeps the safety
// data but drops the author.
func Create(ctx context.Context, pool *pgxpool.Pool, s Submission, userID string) (Report, bool, error) {
	kind, ok := Lookup(s.Kind)
	if !ok {
		return Report{}, false, fmt.Errorf("%w: %q", ErrUnknownKind, s.Kind)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, false, fmt.Errorf("hazard: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	// Look-then-insert would let two simultaneous reports of one hazard both
	// miss each other and both insert. A transaction-scoped advisory lock makes
	// them take turns. Reports are rare, so the serialisation costs nothing.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, mergeLockKey); err != nil {
		return Report{}, false, fmt.Errorf("hazard: locking: %w", err)
	}

	existing, found, err := findMergeTarget(ctx, tx, s, kind)
	if err != nil {
		return Report{}, false, err
	}
	if found {
		rep, err := confirmTx(ctx, tx, existing, userID)
		switch {
		case err == nil:
		case errors.Is(err, ErrOwnReport), errors.Is(err, ErrAlreadyVoted):
			// Reporting a hazard you already reported or confirmed is not an
			// error worth surfacing: the map already shows it. Return it as is.
			rep, err = fetchReport(ctx, tx, existing)
			if err != nil {
				return Report{}, false, err
			}
		default:
			return Report{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Report{}, false, fmt.Errorf("hazard: commit: %w", err)
		}
		return rep, false, nil
	}

	// created_at and expires_at both come from now(), which is fixed for the
	// statement, so the lifetime is exactly the kind's TTL.
	rep, err := scanReport(tx.QueryRow(ctx, `
		INSERT INTO hazard_report (user_id, geom, kind, notes, expires_at)
		VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4, NULLIF($5, ''),
		        now() + make_interval(secs => $6))
		RETURNING `+reportColumns,
		userID, s.Location.Lon, s.Location.Lat, s.Kind, s.Notes, kind.TTL.Seconds()))
	if err != nil {
		return Report{}, false, fmt.Errorf("hazard: creating report: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, false, fmt.Errorf("hazard: commit: %w", err)
	}
	return rep, true, nil
}

// findMergeTarget returns the nearest active hazard of the same kind within
// the kind's merge radius, if any.
func findMergeTarget(ctx context.Context, tx pgx.Tx, s Submission, kind Kind) (int64, bool, error) {
	// A bounding-box pre-filter keeps the GiST index in play; the exact test
	// is geographic, in metres. The box must be at least as wide as the
	// radius in DEGREES OF LONGITUDE, which are shorter than degrees of
	// latitude away from the equator.
	box := degreesFor(kind.MergeRadiusM, s.Location.Lat)

	var id int64
	err := tx.QueryRow(ctx, `
		WITH p AS (SELECT ST_SetSRID(ST_MakePoint($2, $3), 4326) AS g)
		SELECT h.id
		FROM hazard_report h, p
		WHERE h.kind = $1
		  AND h.expires_at > now()
		  AND h.geom && ST_Expand(p.g, $5::double precision)
		  AND ST_DWithin(h.geom::geography, p.g::geography, $4::double precision)
		ORDER BY ST_Distance(h.geom::geography, p.g::geography)
		LIMIT 1`,
		s.Kind, s.Location.Lon, s.Location.Lat, kind.MergeRadiusM, box).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("hazard: looking for a duplicate: %w", err)
	}
	return id, true, nil
}

// degreesFor returns an angle in degrees at least as large as `metres`
// measured east-west at the given latitude, with a margin.
func degreesFor(metres, lat float64) float64 {
	const metresPerDegree = 111_320.0
	c := math.Cos(lat * math.Pi / 180)
	if c < 0.1 { // near the poles, stay finite
		c = 0.1
	}
	return metres / (metresPerDegree * c) * 1.25
}

// ListInBBox returns active hazards inside a bounding box.
func ListInBBox(ctx context.Context, pool *pgxpool.Pool,
	minLon, minLat, maxLon, maxLat float64, limit int) ([]Report, error) {

	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := pool.Query(ctx, `
		SELECT `+reportColumns+`
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
		r, err := scanReport(rows)
		if err != nil {
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
// Each rider can confirm a hazard once, and not their own report. Each
// confirmation extends the hazard's life by its kind's ConfirmExtension, but
// never past created_at + MaxLife.
func Confirm(ctx context.Context, pool *pgxpool.Pool, id int64, userID string) (Report, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("hazard: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	rep, err := confirmTx(ctx, tx, id, userID)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("hazard: commit: %w", err)
	}
	return rep, nil
}

// confirmTx is Confirm inside an existing transaction.
func confirmTx(ctx context.Context, tx pgx.Tx, id int64, userID string) (Report, error) {
	reporter, err := lockActive(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if reporter != nil && *reporter == userID {
		return Report{}, ErrOwnReport
	}
	if err := recordVote(ctx, tx, id, userID, "confirm"); err != nil {
		return Report{}, err
	}

	rep, err := scanReport(tx.QueryRow(ctx, `
		UPDATE hazard_report
		SET confirmations = confirmations + 1,
		    expires_at = LEAST(
		        expires_at + make_interval(secs => coalesce(($2::jsonb -> kind ->> 'extend_s')::double precision, 0)),
		        created_at + make_interval(secs => coalesce(($2::jsonb -> kind ->> 'max_s')::double precision, 0)))
		WHERE id = $1
		RETURNING `+reportColumns, id, lifetimesJSON()))
	if err != nil {
		return Report{}, fmt.Errorf("hazard: confirming report %d: %w", id, err)
	}
	return rep, nil
}

// Resolve records that a rider has found the hazard gone.
//
// The original reporter can take their own report down alone. Otherwise it
// takes ResolveVotesNeeded riders, so one person cannot erase a hazard that
// others are still seeing. A resolved hazard expires immediately but is kept
// as history. The returned report's ExpiresAt shows whether it came down.
func Resolve(ctx context.Context, pool *pgxpool.Pool, id int64, userID string) (Report, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("hazard: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	reporter, err := lockActive(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if err := recordVote(ctx, tx, id, userID, "resolve"); err != nil {
		return Report{}, err
	}

	var votes int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM hazard_vote WHERE hazard_id = $1 AND vote = 'resolve'`, id).
		Scan(&votes); err != nil {
		return Report{}, fmt.Errorf("hazard: counting resolve votes: %w", err)
	}

	if (reporter != nil && *reporter == userID) || votes >= ResolveVotesNeeded {
		if _, err := tx.Exec(ctx,
			`UPDATE hazard_report SET expires_at = now(), resolved_at = now() WHERE id = $1`, id); err != nil {
			return Report{}, fmt.Errorf("hazard: resolving %d: %w", id, err)
		}
	}

	rep, err := fetchReport(ctx, tx, id)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("hazard: commit: %w", err)
	}
	return rep, nil
}

// lockActive locks an active hazard's row and returns its reporter. Locking
// makes two riders resolving at once count each other's votes.
func lockActive(ctx context.Context, tx pgx.Tx, id int64) (*string, error) {
	var reporter *string
	err := tx.QueryRow(ctx, `
		SELECT user_id::text FROM hazard_report
		WHERE id = $1 AND expires_at > now()
		FOR UPDATE`, id).Scan(&reporter)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("hazard: locking %d: %w", id, err)
	}
	return reporter, nil
}

// recordVote stores a rider's single response, or reports that they already
// gave one.
func recordVote(ctx context.Context, tx pgx.Tx, id int64, userID, vote string) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO hazard_vote (hazard_id, user_id, vote) VALUES ($1, $2, $3)
		ON CONFLICT (hazard_id, user_id) DO NOTHING`, id, userID, vote)
	if err != nil {
		return fmt.Errorf("hazard: recording vote: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyVoted
	}
	return nil
}

func fetchReport(ctx context.Context, tx pgx.Tx, id int64) (Report, error) {
	r, err := scanReport(tx.QueryRow(ctx,
		`SELECT `+reportColumns+` FROM hazard_report WHERE id = $1`, id))
	if err != nil {
		return Report{}, fmt.Errorf("hazard: fetching %d: %w", id, err)
	}
	return r, nil
}

func scanReport(row pgx.Row) (Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.Location.Lat, &r.Location.Lon, &r.Kind, &r.Notes,
		&r.Confirmations, &r.CreatedAt, &r.ExpiresAt)
	return r, err
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
