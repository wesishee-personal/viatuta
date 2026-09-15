// Command viatuta is the operator CLI: data ingest and graph building.
//
// These are separate from the API server because they are batch jobs that
// run occasionally, take minutes, and need far more memory than serving a
// request does. Keeping them out of the server process means a monthly data
// refresh cannot affect live routing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/wesishee/viatuta/internal/config"
	"github.com/wesishee/viatuta/internal/crash"
	"github.com/wesishee/viatuta/internal/elevation"
	"github.com/wesishee/viatuta/internal/graph"
	"github.com/wesishee/viatuta/internal/osm"
	"github.com/wesishee/viatuta/internal/routing"
	"github.com/wesishee/viatuta/internal/safety"
	"github.com/wesishee/viatuta/internal/store"
)

// errNotImplemented marks commands whose phase has not been built yet.
var errNotImplemented = errors.New("not implemented yet")

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `viatuta — operator CLI

Usage: viatuta <command> [flags]

Commands:
  ingest-osm         Load an .osm.pbf extract into the routing graph tables   (Phase 2)
  ingest-crashes     Fetch Austin cyclist crash records and match to edges    (Phase 5)
  ingest-elevation   Attach elevation to nodes and grades to edges            (Phase 5)
  score-edges        Recompute derived safety attributes (LTS, crash score)   (Phase 4)
  graph-stats        Print graph size and connectivity diagnostics
  bench-routing      Time random routes on the real graph, with and without A*

Run 'viatuta <command> -h' for command flags.
`)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := cfg.NewLogger()
	slog.SetDefault(logger)

	// Batch jobs can run for minutes; Ctrl-C should stop them cleanly
	// mid-transaction rather than leaving a half-loaded graph behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, rest := args[0], args[1:]

	switch cmd {
	case "ingest-osm":
		fs := flag.NewFlagSet("ingest-osm", flag.ExitOnError)
		input := fs.String("input", "data/austin.osm.pbf", "path to the .osm.pbf extract")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return withDB(ctx, cfg, logger, func(pool *store.Pool) error {
			return ingestOSM(ctx, pool, *input, logger)
		})

	case "ingest-crashes":
		fs := flag.NewFlagSet("ingest-crashes", flag.ExitOnError)
		years := fs.Int("years", 10, "how many years of crash history to load")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return withDB(ctx, cfg, logger, func(pool *store.Pool) error {
			records, err := crash.Fetch(ctx, *years, logger)
			if err != nil {
				return err
			}
			if len(records) == 0 {
				return fmt.Errorf("ingest-crashes: no usable records returned")
			}
			if err := crash.Store(ctx, pool, records, logger); err != nil {
				return err
			}
			return crash.MatchToEdges(ctx, pool, logger)
		})

	case "ingest-elevation":
		fs := flag.NewFlagSet("ingest-elevation", flag.ExitOnError)
		cache := fs.String("cache", "data/elevation", "directory for downloaded elevation tiles")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return withDB(ctx, cfg, logger, func(pool *store.Pool) error {
			if err := elevation.Apply(ctx, pool, *cache, logger); err != nil {
				return err
			}
			st, err := elevation.Summarise(ctx, pool)
			if err != nil {
				return err
			}
			fmt.Printf("\nmean |grade|:     %.2f%%\n", st.Mean)
			fmt.Printf("steepest climb:   %.1f%%\n", st.MaxClimb)
			fmt.Printf("steepest descent: %.1f%%\n", st.MaxDescent)
			fmt.Printf("edges over 8%%:    %d\n", st.SteepClimbs)
			return nil
		})

	case "score-edges":
		return withDB(ctx, cfg, logger, func(pool *store.Pool) error {
			return scoreEdges(ctx, pool, logger)
		})

	case "bench-routing":
		fs := flag.NewFlagSet("bench-routing", flag.ExitOnError)
		n := fs.Int("n", 60, "number of random routes to time")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return withDB(ctx, cfg, logger, func(pool *store.Pool) error {
			return benchRouting(ctx, pool, *n, logger)
		})

	case "graph-stats":
		fs := flag.NewFlagSet("graph-stats", flag.ExitOnError)
		connectivity := fs.Bool("connectivity", true, "also analyse connected components")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return withDB(ctx, cfg, logger, func(pool *store.Pool) error {
			if err := graphStats(ctx, pool); err != nil {
				return err
			}
			if !*connectivity {
				return nil
			}
			return connectivityStats(ctx, pool, logger)
		})

	case "-h", "--help", "help":
		usage()
		return nil

	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// withDB opens a pool, runs fn, and closes the pool afterwards.
//
// Passing a function into a helper that owns setup and teardown is how Go
// handles what other languages do with `with` blocks or try-with-resources.
func withDB(ctx context.Context, cfg config.Config, logger *slog.Logger, fn func(*store.Pool) error) error {
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return fn(pool)
}

// ingestOSM loads an .osm.pbf extract into routing_node and routing_edge.
//
// The three stages are deliberately separate and each is timed: when a run
// looks wrong, the stage timings usually say whether the problem is the
// extract, the tag rules, or the database.
func ingestOSM(ctx context.Context, pool *store.Pool, path string, logger *slog.Logger) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("ingest-osm: %w (run `make data` to download the extract)", err)
	}
	logger.Info("ingesting osm extract", "path", path, "size_mb", info.Size()/(1<<20))

	overall := time.Now()

	start := time.Now()
	scan, err := osm.ScanWays(ctx, path, logger)
	if err != nil {
		return err
	}
	logger.Info("pass 1 complete", "duration", time.Since(start).Round(time.Millisecond))

	start = time.Now()
	nodes, err := osm.ScanNodes(ctx, path, scan, logger)
	if err != nil {
		return err
	}
	logger.Info("pass 2 complete", "duration", time.Since(start).Round(time.Millisecond))

	start = time.Now()
	g := osm.Build(scan, nodes, logger)
	logger.Info("graph built", "duration", time.Since(start).Round(time.Millisecond))

	// The scan results are large; let the collector reclaim them before the
	// database load rather than holding everything at once.
	scan, nodes = nil, nil
	runtime.GC()

	if len(g.Edges) == 0 {
		return fmt.Errorf("ingest-osm: produced no edges — check the extract and the tag filter")
	}

	if err := osm.Load(ctx, pool, g, logger); err != nil {
		return err
	}

	logger.Info("ingest complete",
		"nodes", len(g.Nodes),
		"edges", len(g.Edges),
		"total_duration", time.Since(overall).Round(time.Millisecond),
	)
	return graphStats(ctx, pool)
}

// scoreEdges recomputes every edge's Level of Traffic Stress and stores it.
//
// This runs as a separate command rather than during ingest because the
// safety model is tuned far more often than the map is re-downloaded: after
// editing internal/safety, re-scoring takes seconds where a full re-ingest
// would repeat work that has not changed.
func scoreEdges(ctx context.Context, pool *store.Pool, logger *slog.Logger) error {
	g, err := graph.Load(ctx, pool, logger)
	if err != nil {
		return err
	}

	start := time.Now()
	safety.ScoreGraph(g)
	logger.Info("classified edges", "duration", time.Since(start).Round(time.Millisecond))

	if err := safety.PersistLTS(ctx, pool, g, logger); err != nil {
		return err
	}

	// Print the distribution: it is the fastest way to tell whether a change
	// to the rules did what was intended.
	var counts [5]int
	var lengths [5]float64
	var total float64
	for e := int32(0); e < int32(g.NumEdges()); e++ {
		lts := g.LTS[e]
		if int(lts) >= len(counts) {
			lts = 0
		}
		counts[lts]++
		lengths[lts] += float64(g.LengthM[e])
		total += float64(g.LengthM[e])
	}

	fmt.Printf("\nLevel of Traffic Stress distribution\n")
	fmt.Printf("%-6s %10s %12s %8s\n", "level", "edges", "km", "pct")
	for i := 1; i <= 4; i++ {
		fmt.Printf("LTS %d  %10d %12.1f %7.1f%%\n",
			i, counts[i], lengths[i]/1000, 100*lengths[i]/total)
	}
	if counts[0] > 0 {
		fmt.Printf("unscored %8d %12.1f\n", counts[0], lengths[0]/1000)
	}
	return nil
}

// benchRouting times real routes on the real graph.
//
// Synthetic grids flatter A* — every direction looks equally promising, so
// the heuristic has little to prune. Whether it earns its overhead has to be
// measured on the actual street network.
func benchRouting(ctx context.Context, pool *store.Pool, n int, logger *slog.Logger) error {
	g, err := graph.Load(ctx, pool, logger)
	if err != nil {
		return err
	}

	rng := rand.New(rand.NewSource(42))
	pairs := make([][2]int32, 0, n)
	for len(pairs) < n {
		a := int32(rng.Intn(g.NumNodes()))
		b := int32(rng.Intn(g.NumNodes()))
		if a != b && g.OutDegree(a) > 0 && g.OutDegree(b) > 0 {
			pairs = append(pairs, [2]int32{a, b})
		}
	}

	model := safety.NewModel(safety.Comfortable)
	base := routing.Options{
		EdgeCost: model.EdgeCost,
		TurnCost: model.TurnCost,
		Allow:    model.Allow,
		Over:     model.Over,
		BudgetM:  safety.Comfortable.StressBudgetM,
	}

	run := func(label string, opt routing.Options) {
		var totalNs int64
		var totalStates, ok int
		var times []float64

		for _, p := range pairs {
			t0 := time.Now()
			path, err := routing.Search(g, p[0], p[1], opt)
			d := time.Since(t0)
			if err != nil {
				continue
			}
			ok++
			totalNs += d.Nanoseconds()
			totalStates += path.Expansions
			times = append(times, float64(d.Nanoseconds())/1e6)
		}
		if ok == 0 {
			fmt.Printf("%-22s no routes found\n", label)
			return
		}
		sort.Float64s(times)
		fmt.Printf("%-22s %5d routed  mean %7.1f ms  p50 %7.1f ms  p95 %7.1f ms  mean states %9d\n",
			label, ok, float64(totalNs)/float64(ok)/1e6,
			times[len(times)/2], times[int(float64(len(times))*0.95)],
			totalStates/ok)
	}

	noHeuristic := base
	noHeuristic.NoHeuristic = true

	fmt.Printf("\nrouting %d random pairs across Austin (comfortable profile)\n\n", n)
	run("dijkstra", noHeuristic)
	run("a-star", base)

	// Without a stress budget the state space is a quarter the size, which
	// isolates how much the budget dimension itself costs.
	noBudget := base
	noBudget.Over, noBudget.BudgetM = nil, 0
	noBudgetPlain := noBudget
	noBudgetPlain.NoHeuristic = true
	fmt.Println()
	run("dijkstra, no budget", noBudgetPlain)
	run("a-star, no budget", noBudget)

	return nil
}

// connectivityStats loads the graph and reports how it breaks into islands.
//
// A road network should be one dominant component. Many large components
// means the ingest dropped connections; a long tail of tiny ones is normal
// and usually reflects genuinely isolated features such as a gated
// development or a trail clipped by the extract boundary.
func connectivityStats(ctx context.Context, pool *store.Pool, logger *slog.Logger) error {
	g, err := graph.Load(ctx, pool, logger)
	if err != nil {
		return err
	}

	c := g.WeaklyConnectedComponents()
	fmt.Printf("\ncomponents:       %d\n", c.Count)
	fmt.Printf("largest:          %d nodes (%.2f%% of graph)\n", c.LargestSize, c.LargestPct)
	fmt.Printf("single-node:      %d\n", c.Isolated)
	fmt.Printf("ten largest:      %v\n", c.TopSizes)

	// The same measurement per stress level. Note this is reachability with
	// NO stress budget — the strict lower bound. Profiles with a budget
	// reach considerably more, because the budget is a whole-route bound
	// rather than a property of any subgraph and so cannot be measured here.
	fmt.Printf("\nreachability by traffic stress, with zero stress budget (lower bound)\n")
	fmt.Printf("%-10s %14s %12s %12s\n", "max LTS", "largest comp", "pct of graph", "components")
	for lts := uint8(1); lts <= 4; lts++ {
		s := g.ComponentsUpToLTS(lts)
		fmt.Printf("LTS <= %d   %14d %11.2f%% %12d\n", lts, s.LargestSize, s.LargestPct, s.Count)
	}

	// A healthy metro extract keeps nearly everything in one piece.
	switch {
	case c.LargestPct < 80:
		fmt.Printf("\nWARNING: the graph is fragmented; routing will fail between components.\n")
	case c.LargestPct < 95:
		fmt.Printf("\nNote: a noticeable share of the graph is unreachable from the main component.\n")
	}
	return nil
}

// graphStats prints basic counts — the fastest way to tell whether an ingest
// produced something plausible.
func graphStats(ctx context.Context, pool *store.Pool) error {
	var nodes, edges int64
	var avgLen, totalKm *float64

	err := pool.QueryRow(ctx, `SELECT count(*) FROM routing_node`).Scan(&nodes)
	if err != nil {
		return fmt.Errorf("counting nodes: %w", err)
	}
	err = pool.QueryRow(ctx,
		`SELECT count(*), avg(length_m), sum(length_m)/1000.0 FROM routing_edge`,
	).Scan(&edges, &avgLen, &totalKm)
	if err != nil {
		return fmt.Errorf("counting edges: %w", err)
	}

	fmt.Printf("nodes:            %d\n", nodes)
	fmt.Printf("directed edges:   %d\n", edges)
	if avgLen != nil {
		fmt.Printf("mean edge length: %.1f m\n", *avgLen)
	}
	if totalKm != nil {
		fmt.Printf("total length:     %.1f km\n", *totalKm)
	}
	if nodes > 0 {
		fmt.Printf("edges per node:   %.2f\n", float64(edges)/float64(nodes))
	}
	return nil
}
