package crash

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MatchRadiusM is how far from a crash an edge may be and still be
// attributed with it.
//
// Crash records are positioned at the nearest address or intersection, not at
// the point of impact, so their accuracy is roughly +/-30 m. Matching within
// 25 m therefore attributes a crash to the junction area rather than to one
// precise segment — which is the honest resolution of this data, and why the
// result is treated as a neighbourhood signal rather than a per-metre one.
const MatchRadiusM = 25

// matchRadiusDeg is MatchRadiusM expressed in degrees, for the indexed
// bounding-box pre-filter. At Austin's latitude one degree of longitude is
// about 96 km, so 25 m is roughly 0.00026 degrees; 0.0004 is deliberately
// generous, since the exact geography test refines it afterwards.
const matchRadiusDeg = 0.0004

// Severity weights on the KABCO scale. A fatality is not merely "worse" than
// a possible injury; the gap is the whole point of weighting at all.
var severityWeight = [5]float64{0.5, 1.0, 2.5, 6.0, 12.0}

// HalfLifeYears is how quickly a crash's influence decays.
//
// Road layouts change, bike lanes get built, and a crash on a street that has
// since been rebuilt says little about riding it today. Four years is a
// compromise: short enough to track real change, long enough that the 2,500
// usable Austin records are not reduced to statistical noise.
const HalfLifeYears = 4.0

// Store writes crash records, replacing any previous import from the same
// source.
func Store(ctx context.Context, pool *pgxpool.Pool, records []Record, logger *slog.Logger) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("crash: beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// CASCADE clears crash_edge, whose rows reference the ids being replaced.
	if _, err := tx.Exec(ctx, `DELETE FROM crash WHERE source = 'austin_cris'`); err != nil {
		return fmt.Errorf("crash: clearing previous import: %w", err)
	}

	rows := make([][]any, 0, len(records))
	for _, r := range records {
		rows = append(rows, []any{
			"austin_cris", r.SourceID, r.Lon, r.Lat,
			r.Occurred, r.Severity, true, string(r.Raw),
		})
	}

	// CopyFrom cannot build a geometry directly, so the rows land in a
	// staging table and one INSERT..SELECT converts the coordinates.
	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE crash_stage (
			source text, source_id text, lon double precision, lat double precision,
			occurred_at timestamptz, severity smallint, cyclist boolean, raw jsonb
		) ON COMMIT DROP`); err != nil {
		return fmt.Errorf("crash: creating staging table: %w", err)
	}

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"crash_stage"},
		[]string{"source", "source_id", "lon", "lat", "occurred_at", "severity", "cyclist", "raw"},
		pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("crash: copying records: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO crash (source, source_id, geom, occurred_at, severity, cyclist_involved, raw)
		SELECT source, source_id, ST_SetSRID(ST_MakePoint(lon, lat), 4326),
		       occurred_at, severity, cyclist, raw
		FROM crash_stage
		ON CONFLICT (source, source_id) DO NOTHING`)
	if err != nil {
		return fmt.Errorf("crash: inserting records: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("crash: committing: %w", err)
	}

	logger.Info("stored crash records", "inserted", tag.RowsAffected(), "fetched", len(records))
	return nil
}

// MatchToEdges attributes every crash to the edges near it and recomputes
// each edge's crash pressure.
//
// Both directions of a street are matched, because a crash makes a road more
// dangerous regardless of which way a rider is travelling. Cross streets at a
// junction are matched too, which is intended: at the positional accuracy
// this data has, a crash is evidence about the junction rather than about one
// approach to it.
func MatchToEdges(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	start := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("crash: beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `TRUNCATE crash_edge`); err != nil {
		return fmt.Errorf("crash: clearing matches: %w", err)
	}

	// The spatial join, in two stages.
	//
	// The `&&` bounding-box test comes first and is the whole reason this
	// runs in seconds rather than hours: routing_edge's GIST index is on
	// `geom`, which is geometry, so a predicate written directly against
	// geography cannot use it and degrades to scanning 400k edges per
	// crash. The cheap indexed test narrows to a handful of candidates,
	// and only those pay for the accurate geography distance.
	tag, err := tx.Exec(ctx, `
		INSERT INTO crash_edge (crash_id, edge_id, distance_m)
		SELECT c.id, e.id, ST_Distance(c.geom::geography, e.geom::geography)
		FROM crash c
		JOIN routing_edge e
		  ON e.geom && ST_Expand(c.geom, $2)
		 AND ST_DWithin(c.geom::geography, e.geom::geography, $1)
		WHERE c.cyclist_involved
		ON CONFLICT DO NOTHING`, MatchRadiusM, matchRadiusDeg)
	if err != nil {
		return fmt.Errorf("crash: matching to edges: %w", err)
	}

	// Aggregate into per-edge pressure: severity-weighted, recency-decayed
	// crashes per 100 m of road, then squashed into 0..1.
	//
	// Four factors, each correcting a specific way the raw counts mislead:
	//
	//  1. Severity, on the KABCO scale. A fatality is not merely worse than
	//     a possible injury; the gap is why weighting exists at all.
	//  2. Recency, halving every HalfLifeYears, because roads get rebuilt.
	//  3. Distance decay. A crash is evidence mainly about the edge it sits
	//     on. Without this, the +/-30 m positional accuracy smears every
	//     arterial crash across all of its cross streets equally.
	//  4. A discount for separated infrastructure. This one was added after
	//     the first run scored LTS 1 as more dangerous than LTS 2: protected
	//     tracks running alongside arterials were inheriting the roadway's
	//     crash record. A motor-vehicle collision in the carriageway is weak
	//     evidence about the track that exists to keep riders out of it. The
	//     discount is partial, not total, because riders genuinely are struck
	//     on tracks at driveway and junction crossings.
	//
	// Dividing by length matters too. Three crashes along 500 m is a
	// different road from three crashes on a 30 m junction approach, and
	// counting alone would rate them the same.
	//
	// The 1 - exp(-x/k) curve saturates, so one catastrophic junction cannot
	// dominate the whole cost function no matter how many crashes it holds.
	if _, err := tx.Exec(ctx, `
		WITH pressure AS (
			SELECT ce.edge_id,
			       sum(
			           (ARRAY[0.5,1.0,2.5,6.0,12.0])[c.severity + 1]
			           * power(0.5, EXTRACT(EPOCH FROM (now() - c.occurred_at))
			                        / (365.25 * 86400) / $1)
			           * exp(-ce.distance_m / $3)
			           * CASE WHEN e.infra_class IN ('protected_track', 'path')
			                  THEN $4 ELSE 1.0 END
			       ) AS weighted
			FROM crash_edge ce
			JOIN crash c ON c.id = ce.crash_id
			JOIN routing_edge e ON e.id = ce.edge_id
			GROUP BY ce.edge_id
		)
		UPDATE routing_edge e
		SET crash_score = LEAST(1.0, 1.0 - exp(-(p.weighted / GREATEST(e.length_m / 100.0, 0.3)) / $2))
		FROM pressure p
		WHERE e.id = p.edge_id`,
		HalfLifeYears, saturationConstant, distanceDecayM, separatedDiscount); err != nil {
		return fmt.Errorf("crash: computing pressure: %w", err)
	}

	// Edges with no matched crashes must be reset, or a previous import's
	// scores would linger after the data changed.
	if _, err := tx.Exec(ctx, `
		UPDATE routing_edge e SET crash_score = 0
		WHERE e.crash_score > 0
		  AND NOT EXISTS (SELECT 1 FROM crash_edge ce WHERE ce.edge_id = e.id)`); err != nil {
		return fmt.Errorf("crash: resetting unmatched edges: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("crash: committing: %w", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE routing_edge, crash_edge`); err != nil {
		return fmt.Errorf("crash: analyzing: %w", err)
	}

	logger.Info("matched crashes to edges",
		"matches", tag.RowsAffected(),
		"radius_m", MatchRadiusM,
		"duration", time.Since(start).Round(time.Millisecond))
	return nil
}

const (
	// saturationConstant sets where the crash-pressure curve bends. A
	// weighted pressure equal to this value scores about 0.63; twice it
	// scores 0.86. Calibrated so a junction with a couple of recent injury
	// crashes lands mid-range rather than pinned at the top.
	saturationConstant = 6.0

	// distanceDecayM is the e-folding distance for attributing a crash to an
	// edge. At 12 m, the segment a crash sits on takes nearly full weight
	// while one 25 m away takes about an eighth.
	distanceDecayM = 12.0

	// separatedDiscount is the share of a nearby crash attributed to
	// physically separated infrastructure. See the note in MatchToEdges.
	separatedDiscount = 0.25
)
