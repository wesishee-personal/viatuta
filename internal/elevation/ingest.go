package elevation

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// MinGradeLengthM is the shortest edge we will compute a gradient for.
	//
	// Elevation data has vertical noise of a metre or so. Over a 10 m edge
	// that noise alone reads as a 10% gradient, which would litter the graph
	// with imaginary cliffs at every junction. Below this length the
	// gradient is recorded as zero rather than invented.
	MinGradeLengthM = 20.0

	// MaxGradePct clamps implausible results. Austin's steepest rideable
	// streets are around 15%; anything past 25% is a data artifact, usually
	// a bridge or tunnel whose deck height differs from the ground the
	// elevation model reports.
	MaxGradePct = 25.0
)

// Apply downloads elevation for the graph's extent, samples every node, and
// derives each edge's gradient.
func Apply(ctx context.Context, pool *pgxpool.Pool, cacheDir string, logger *slog.Logger) error {
	start := time.Now()

	var minLat, minLon, maxLat, maxLon float64
	err := pool.QueryRow(ctx, `
		SELECT ST_YMin(e), ST_XMin(e), ST_YMax(e), ST_XMax(e)
		FROM (SELECT ST_Extent(geom) AS e FROM routing_node) s`).
		Scan(&minLat, &minLon, &maxLat, &maxLon)
	if err != nil {
		return fmt.Errorf("elevation: reading graph extent: %w", err)
	}
	logger.Info("graph extent",
		"min_lat", minLat, "min_lon", minLon, "max_lat", maxLat, "max_lon", maxLon)

	tiles := New(cacheDir)
	if err := tiles.Cover(ctx, minLat, minLon, maxLat, maxLon, logger); err != nil {
		return err
	}

	if err := applyNodes(ctx, pool, tiles, logger); err != nil {
		return err
	}
	if err := applyGrades(ctx, pool, logger); err != nil {
		return err
	}

	logger.Info("elevation applied", "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

// applyNodes samples a height for every node and writes it back.
func applyNodes(ctx context.Context, pool *pgxpool.Pool, tiles *Tiles, logger *slog.Logger) error {
	rows, err := pool.Query(ctx, `SELECT id, ST_Y(geom), ST_X(geom) FROM routing_node ORDER BY id`)
	if err != nil {
		return fmt.Errorf("elevation: querying nodes: %w", err)
	}

	type sample struct {
		id  int64
		ele float32
	}
	var samples []sample
	var missing int

	for rows.Next() {
		var id int64
		var lat, lon float64
		if err := rows.Scan(&id, &lat, &lon); err != nil {
			rows.Close()
			return fmt.Errorf("elevation: scanning node: %w", err)
		}
		ele, ok := tiles.At(lat, lon)
		if !ok {
			missing++
			continue
		}
		samples = append(samples, sample{id, ele})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("elevation: reading nodes: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("elevation: beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`CREATE TEMP TABLE node_ele (id bigint PRIMARY KEY, ele real) ON COMMIT DROP`); err != nil {
		return fmt.Errorf("elevation: creating temp table: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		var buf []byte
		for _, s := range samples {
			buf = buf[:0]
			buf = strconv.AppendInt(buf, s.id, 10)
			buf = append(buf, '\t')
			buf = strconv.AppendFloat(buf, float64(s.ele), 'f', 2, 64)
			buf = append(buf, '\n')
			if _, err := pw.Write(buf); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.Close()
	}()

	if _, err := tx.Conn().PgConn().CopyFrom(ctx, pr, `COPY node_ele (id, ele) FROM STDIN`); err != nil {
		return fmt.Errorf("elevation: copying node heights: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE routing_node n SET elevation_m = t.ele FROM node_ele t WHERE n.id = t.id`); err != nil {
		return fmt.Errorf("elevation: updating nodes: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("elevation: committing node heights: %w", err)
	}

	logger.Info("sampled node elevations", "nodes", len(samples), "missing", missing)
	return nil
}

// applyGrades derives each edge's signed gradient from its endpoints.
//
// The gradient is signed IN THE DIRECTION OF TRAVEL, which is the entire
// reason edges are stored directed: the same hill is a climb one way and a
// descent the other, and the cost model treats those differently.
func applyGrades(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	tag, err := pool.Exec(ctx, `
		UPDATE routing_edge e
		-- The casts are required: Postgres cannot infer a type for a bare
		-- negated parameter, and reports "operator is not unique" instead.
		SET grade_pct = GREATEST(-$1::double precision, LEAST($1::double precision,
		        (nt.elevation_m - nf.elevation_m) / e.length_m * 100))
		FROM routing_node nf, routing_node nt
		WHERE nf.id = e.from_node
		  AND nt.id = e.to_node
		  AND nf.elevation_m IS NOT NULL
		  AND nt.elevation_m IS NOT NULL
		  AND e.length_m >= $2::double precision`, MaxGradePct, MinGradeLengthM)
	if err != nil {
		return fmt.Errorf("elevation: computing grades: %w", err)
	}

	// Edges too short to measure get an explicit zero rather than being
	// left null, so the cost model never has to special-case them.
	if _, err := pool.Exec(ctx,
		`UPDATE routing_edge SET grade_pct = 0 WHERE grade_pct IS NULL`); err != nil {
		return fmt.Errorf("elevation: zeroing short edges: %w", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE routing_edge, routing_node`); err != nil {
		return fmt.Errorf("elevation: analyzing: %w", err)
	}

	logger.Info("computed edge grades", "edges", tag.RowsAffected())
	return nil
}

// GradeStats summarises the gradient distribution, for verification.
type GradeStats struct {
	Mean, MaxClimb, MaxDescent float64
	SteepClimbs                int
}

// Summarise reads back the gradient distribution.
func Summarise(ctx context.Context, pool *pgxpool.Pool) (GradeStats, error) {
	var s GradeStats
	err := pool.QueryRow(ctx, `
		SELECT coalesce(avg(abs(grade_pct)), 0), coalesce(max(grade_pct), 0),
		       coalesce(min(grade_pct), 0), count(*) FILTER (WHERE grade_pct > 8)
		FROM routing_edge`).
		Scan(&s.Mean, &s.MaxClimb, &s.MaxDescent, &s.SteepClimbs)
	if err != nil {
		return s, fmt.Errorf("elevation: summarising grades: %w", err)
	}
	s.Mean = math.Round(s.Mean*100) / 100
	return s, nil
}
