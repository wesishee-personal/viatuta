package osm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
)

// coordPrecision is how many decimal places of latitude/longitude to emit.
// Seven places is about 1 cm — far finer than OSM's own accuracy, and well
// short of float64's limit, so nothing is lost by rounding here.
const coordPrecision = 7

// Load replaces the contents of routing_node and routing_edge with g.
//
// Everything happens in one transaction: either the whole graph lands or none
// of it does. A half-loaded graph is worse than no graph at all, because it
// would route confidently across a city that is missing streets.
func Load(ctx context.Context, pool *pgxpool.Pool, g *Graph, logger *slog.Logger) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("osm: beginning transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this is safe to
	// defer unconditionally — and it guarantees cleanup on every error path.
	defer tx.Rollback(ctx)

	// CASCADE also clears crash_edge, whose rows reference edge ids that are
	// about to be reassigned.
	if _, err := tx.Exec(ctx, `TRUNCATE routing_node, routing_edge CASCADE`); err != nil {
		return fmt.Errorf("osm: truncating: %w", err)
	}

	start := time.Now()
	n, err := copyNodes(ctx, tx, g.Nodes)
	if err != nil {
		return err
	}
	logger.Info("copied nodes", "rows", n, "duration", time.Since(start).Round(time.Millisecond))

	start = time.Now()
	e, err := copyEdges(ctx, tx, g.Edges)
	if err != nil {
		return err
	}
	logger.Info("copied edges", "rows", e, "duration", time.Since(start).Round(time.Millisecond))

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("osm: committing: %w", err)
	}

	// Without fresh statistics the planner still believes both tables are
	// empty, and will pick bad plans for the first queries after an ingest.
	if _, err := pool.Exec(ctx, `ANALYZE routing_node, routing_edge`); err != nil {
		return fmt.Errorf("osm: analyzing: %w", err)
	}
	return nil
}

// copyNodes streams nodes into Postgres using COPY.
//
// COPY is dramatically faster than INSERT for bulk loads: one statement, one
// parse, and no per-row round trip. The rows are streamed through a pipe
// rather than buffered, so memory stays flat no matter how large the city is.
func copyNodes(ctx context.Context, tx pgx.Tx, nodes []Node) (int64, error) {
	const sql = `COPY routing_node (id, osm_node_id, geom, control) FROM STDIN`

	return copyRows(ctx, tx, sql, func(w io.Writer) error {
		var r copyRow
		for _, n := range nodes {
			r.reset()
			r.Int(n.ID)
			r.Int(n.OSMID)
			r.Str(pointEWKT(n.Loc))
			r.Str(n.Control)
			if _, err := w.Write(r.finish()); err != nil {
				return err
			}
		}
		return nil
	})
}

// copyEdges streams edges into Postgres using COPY.
func copyEdges(ctx context.Context, tx pgx.Tx, edges []Edge) (int64, error) {
	const sql = `COPY routing_edge (
		id, osm_way_id, from_node, to_node, geom, length_m,
		highway, name, infra_class, maxspeed_mph, lanes, surface, lit,
		bearing_start, bearing_end
	) FROM STDIN`

	return copyRows(ctx, tx, sql, func(w io.Writer) error {
		var r copyRow
		for _, e := range edges {
			r.reset()
			r.Int(e.ID)
			r.Int(e.WayID)
			r.Int(e.FromNode)
			r.Int(e.ToNode)
			r.Str(lineEWKT(e.Shape))
			r.Float(e.LengthM, 3)
			r.Str(e.Attrs.Highway)
			r.StrOrNull(e.Attrs.Name)
			r.Str(string(e.Attrs.Infra))
			r.OptInt16(e.Attrs.MaxSpeedMPH)
			r.OptInt16(e.Attrs.Lanes)
			r.StrOrNull(e.Attrs.Surface)
			r.OptBool(e.Attrs.Lit)
			r.Float(float64(e.BearingStart), 2)
			r.Float(float64(e.BearingEnd), 2)
			if _, err := w.Write(r.finish()); err != nil {
				return err
			}
		}
		return nil
	})
}

// copyRows runs a COPY ... FROM STDIN, with write supplying the row data.
//
// io.Pipe connects a writer to a reader: the goroutine writes rows on one
// end while Postgres consumes them from the other. Nothing accumulates in
// memory, and closing the pipe with an error propagates that error to the
// database driver so a failed encode aborts the COPY rather than truncating
// it silently.
func copyRows(ctx context.Context, tx pgx.Tx, sql string, write func(io.Writer) error) (int64, error) {
	pr, pw := io.Pipe()

	go func() {
		err := write(pw)
		pw.CloseWithError(err)
	}()

	tag, err := tx.Conn().PgConn().CopyFrom(ctx, pr, sql)
	if err != nil {
		pr.CloseWithError(err)
		return 0, fmt.Errorf("osm: copy failed: %w", err)
	}
	return tag.RowsAffected(), nil
}

// --- COPY text format ----------------------------------------------------

// copyRow builds one tab-separated line in Postgres's COPY text format.
//
// The format is simple but unforgiving: fields are tab-separated, rows are
// newline-terminated, \N means NULL, and any literal backslash, tab, newline
// or carriage return inside a value must be escaped. A street name containing
// a stray tab would otherwise shift every later column by one.
type copyRow struct {
	buf   bytes.Buffer
	first bool
}

func (r *copyRow) reset() {
	r.buf.Reset()
	r.first = true
}

func (r *copyRow) sep() {
	if !r.first {
		r.buf.WriteByte('\t')
	}
	r.first = false
}

func (r *copyRow) Str(s string) {
	r.sep()
	writeEscaped(&r.buf, s)
}

// StrOrNull writes NULL for an empty string, preserving the distinction
// between "no name recorded" and "named the empty string".
func (r *copyRow) StrOrNull(s string) {
	if s == "" {
		r.Null()
		return
	}
	r.Str(s)
}

func (r *copyRow) Null() {
	r.sep()
	r.buf.WriteString(`\N`)
}

func (r *copyRow) Int(v int64) {
	r.sep()
	r.buf.WriteString(strconv.FormatInt(v, 10))
}

func (r *copyRow) Float(v float64, prec int) {
	r.sep()
	r.buf.WriteString(strconv.FormatFloat(v, 'f', prec, 64))
}

func (r *copyRow) OptInt16(v *int16) {
	if v == nil {
		r.Null()
		return
	}
	r.Int(int64(*v))
}

func (r *copyRow) OptBool(v *bool) {
	if v == nil {
		r.Null()
		return
	}
	r.sep()
	if *v {
		r.buf.WriteByte('t')
	} else {
		r.buf.WriteByte('f')
	}
}

// finish terminates the row and returns its bytes. The slice is only valid
// until the next reset, which is fine because callers write it immediately.
func (r *copyRow) finish() []byte {
	r.buf.WriteByte('\n')
	return r.buf.Bytes()
}

func writeEscaped(buf *bytes.Buffer, s string) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			buf.WriteString(`\\`)
		case '\t':
			buf.WriteString(`\t`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		default:
			buf.WriteByte(c)
		}
	}
}

// --- geometry encoding ---------------------------------------------------

// pointEWKT renders a coordinate as extended well-known text, which PostGIS
// parses directly on input. Note the lon-lat ordering, opposite to LatLon's
// field order.
func pointEWKT(p geo.LatLon) string {
	var b bytes.Buffer
	b.WriteString("SRID=4326;POINT(")
	writeCoord(&b, p)
	b.WriteByte(')')
	return b.String()
}

// lineEWKT renders an edge's geometry as a LINESTRING.
func lineEWKT(shape []geo.LatLon) string {
	var b bytes.Buffer
	b.Grow(len(shape) * 24)
	b.WriteString("SRID=4326;LINESTRING(")
	for i, p := range shape {
		if i > 0 {
			b.WriteByte(',')
		}
		writeCoord(&b, p)
	}
	b.WriteByte(')')
	return b.String()
}

func writeCoord(b *bytes.Buffer, p geo.LatLon) {
	b.WriteString(strconv.FormatFloat(p.Lon, 'f', coordPrecision, 64))
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(p.Lat, 'f', coordPrecision, 64))
}
