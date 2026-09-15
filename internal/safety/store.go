package safety

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/graph"
)

// PersistLTS writes computed LTS values back to routing_edge.
//
// Updating 400k rows one statement at a time would take minutes. Instead the
// values are COPYed into a temporary table and applied with a single joined
// UPDATE, which Postgres executes as one pass.
func PersistLTS(ctx context.Context, pool *pgxpool.Pool, g *graph.Graph, logger *slog.Logger) error {
	start := time.Now()

	// An explicit transaction is required, not merely tidy: ON COMMIT DROP
	// is what cleans the temp table up, and outside a transaction every
	// statement commits on its own — which would drop the table the instant
	// it was created.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("safety: beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`CREATE TEMP TABLE edge_lts (id bigint PRIMARY KEY, lts smallint) ON COMMIT DROP`); err != nil {
		return fmt.Errorf("safety: creating temp table: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		var buf []byte
		for e := int32(0); e < int32(g.NumEdges()); e++ {
			buf = buf[:0]
			buf = strconv.AppendInt(buf, g.DBID[e], 10)
			buf = append(buf, '\t')
			buf = strconv.AppendInt(buf, int64(g.LTS[e]), 10)
			buf = append(buf, '\n')
			if _, err := pw.Write(buf); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.Close()
	}()

	if _, err := tx.Conn().PgConn().CopyFrom(ctx, pr, `COPY edge_lts (id, lts) FROM STDIN`); err != nil {
		return fmt.Errorf("safety: copying lts values: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE routing_edge e
		SET lts = t.lts, updated_at = now()
		FROM edge_lts t
		WHERE e.id = t.id AND e.lts IS DISTINCT FROM t.lts`)
	if err != nil {
		return fmt.Errorf("safety: applying lts values: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("safety: committing: %w", err)
	}

	// ANALYZE runs outside the transaction so the new statistics are
	// visible to every session immediately.
	if _, err := pool.Exec(ctx, `ANALYZE routing_edge`); err != nil {
		return fmt.Errorf("safety: analyzing: %w", err)
	}

	logger.Info("persisted lts",
		"updated", tag.RowsAffected(),
		"total", g.NumEdges(),
		"duration", time.Since(start).Round(time.Millisecond))
	return nil
}
