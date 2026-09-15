package graph

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Load reads the whole graph from Postgres into memory.
//
// The ORDER BY is load-bearing, not cosmetic: the CSR layout requires edges
// grouped by source node, and sorting in Postgres avoids sorting 400k rows
// again in Go.
func Load(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (*Graph, error) {
	start := time.Now()

	g := &Graph{}
	if err := loadNodes(ctx, pool, g); err != nil {
		return nil, err
	}
	if err := loadEdges(ctx, pool, g); err != nil {
		return nil, err
	}
	g.buildOffsets()
	g.ComputeNodeMaxLTS()
	if err := g.buildDBIndex(); err != nil {
		return nil, err
	}

	logger.Info("graph loaded",
		"nodes", g.NumNodes(),
		"edges", g.NumEdges(),
		"duration", time.Since(start).Round(time.Millisecond),
		"approx_mb", g.ApproxBytes()/(1<<20),
	)
	return g, nil
}

func loadNodes(ctx context.Context, pool *pgxpool.Pool, g *Graph) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM routing_node`).Scan(&n); err != nil {
		return fmt.Errorf("graph: counting nodes: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("graph: routing_node is empty — run `make ingest` first")
	}

	g.NodeLat = make([]float64, n)
	g.NodeLon = make([]float64, n)
	g.NodeControl = make([]Control, n)

	// Selecting ST_X/ST_Y rather than the geometry itself keeps the wire
	// format to plain floats, so no PostGIS-aware decoding is needed.
	rows, err := pool.Query(ctx,
		`SELECT id, ST_X(geom), ST_Y(geom), control FROM routing_node ORDER BY id`)
	if err != nil {
		return fmt.Errorf("graph: querying nodes: %w", err)
	}
	defer rows.Close()

	var seen int
	for rows.Next() {
		var id int64
		var lon, lat float64
		var control string
		if err := rows.Scan(&id, &lon, &lat, &control); err != nil {
			return fmt.Errorf("graph: scanning node: %w", err)
		}
		if id < 0 || id >= int64(n) {
			return fmt.Errorf("graph: node id %d outside 0..%d — ids must be dense", id, n-1)
		}
		g.NodeLat[id] = lat
		g.NodeLon[id] = lon
		g.NodeControl[id] = ParseControl(control)
		seen++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graph: reading nodes: %w", err)
	}
	if seen != n {
		return fmt.Errorf("graph: expected %d nodes, read %d", n, seen)
	}
	return nil
}

func loadEdges(ctx context.Context, pool *pgxpool.Pool, g *Graph) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM routing_edge`).Scan(&n); err != nil {
		return fmt.Errorf("graph: counting edges: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("graph: routing_edge is empty — run `make ingest` first")
	}

	g.From = make([]int32, 0, n)
	g.To = make([]int32, 0, n)
	g.LengthM = make([]float32, 0, n)
	g.DBID = make([]int64, 0, n)
	g.Infra = make([]Infra, 0, n)
	g.Highway = make([]Highway, 0, n)
	g.Surface = make([]Surface, 0, n)
	g.MaxSpeed = make([]int16, 0, n)
	g.Lanes = make([]int8, 0, n)
	g.Lit = make([]int8, 0, n)
	g.LTS = make([]uint8, 0, n)
	g.CrashScore = make([]float32, 0, n)
	g.GradePct = make([]float32, 0, n)
	g.BearingStart = make([]float32, 0, n)
	g.BearingEnd = make([]float32, 0, n)

	rows, err := pool.Query(ctx, `
		SELECT id, from_node, to_node, length_m, highway, infra_class,
		       maxspeed_mph, lanes, lit, lts, crash_score, grade_pct,
		       coalesce(surface, ''), coalesce(bearing_start, 0), coalesce(bearing_end, 0)
		FROM routing_edge
		ORDER BY from_node, id`)
	if err != nil {
		return fmt.Errorf("graph: querying edges: %w", err)
	}
	defer rows.Close()

	nodeCount := int32(g.NumNodes())
	for rows.Next() {
		var (
			id, from, to      int64
			length            float64
			highway, infra    string
			speed, lanes, lts *int16
			lit               *bool
			crash, grade      *float32
			surface           string
			bStart, bEnd      float32
		)
		if err := rows.Scan(&id, &from, &to, &length, &highway, &infra,
			&speed, &lanes, &lit, &lts, &crash, &grade,
			&surface, &bStart, &bEnd); err != nil {
			return fmt.Errorf("graph: scanning edge: %w", err)
		}
		if from < 0 || from >= int64(nodeCount) || to < 0 || to >= int64(nodeCount) {
			return fmt.Errorf("graph: edge %d references a node outside 0..%d", id, nodeCount-1)
		}

		g.From = append(g.From, int32(from))
		g.To = append(g.To, int32(to))
		g.LengthM = append(g.LengthM, float32(length))
		g.DBID = append(g.DBID, id)
		g.Infra = append(g.Infra, ParseInfra(infra))
		g.Highway = append(g.Highway, ParseHighway(highway))
		g.Surface = append(g.Surface, ParseSurface(surface))
		g.MaxSpeed = append(g.MaxSpeed, optInt16(speed, SpeedUnknown))
		g.Lanes = append(g.Lanes, int8(optInt16(lanes, int16(LanesUnknown))))
		g.Lit = append(g.Lit, optLit(lit))
		g.LTS = append(g.LTS, uint8(optInt16(lts, 0)))
		g.CrashScore = append(g.CrashScore, optFloat32(crash))
		g.GradePct = append(g.GradePct, optFloat32(grade))
		g.BearingStart = append(g.BearingStart, bStart)
		g.BearingEnd = append(g.BearingEnd, bEnd)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graph: reading edges: %w", err)
	}
	return nil
}

// buildOffsets computes the CSR index from the From column.
//
// This relies on the rows having arrived sorted by source node. The check
// below turns a silently wrong graph — where a node's out-edges are the wrong
// range entirely — into a loud startup failure.
func (g *Graph) buildOffsets() {
	n := g.NumNodes()
	g.Offs = make([]int32, n+1)

	for i, from := range g.From {
		if i > 0 && from < g.From[i-1] {
			panic("graph: edges are not sorted by source node")
		}
		g.Offs[from+1]++
	}
	for i := 1; i <= n; i++ {
		g.Offs[i] += g.Offs[i-1]
	}
}

// ApproxBytes estimates the graph's heap footprint, so capacity planning does
// not have to be guesswork.
func (g *Graph) ApproxBytes() int {
	n, e := g.NumNodes(), g.NumEdges()
	perNode := 8 + 8 + 1 + 4 // lat, lon, control, offset
	perEdge := 4 + 4 + 4 + 8 + 1 + 1 + 1 + 2 + 1 + 1 + 1 + 4 + 4 + 4 + 4
	return n*perNode + e*perEdge
}

func optInt16(v *int16, def int16) int16 {
	if v == nil {
		return def
	}
	return *v
}

func optFloat32(v *float32) float32 {
	if v == nil {
		return 0
	}
	return *v
}

func optLit(v *bool) int8 {
	if v == nil {
		return LitUnknown
	}
	if *v {
		return LitYes
	}
	return LitNo
}
