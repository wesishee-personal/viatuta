// Package hazard handles rider-submitted road hazards.
//
// Hazards differ from every other signal in this system: they are live. Crash
// records and elevation are refreshed by a batch job, but a rider reporting
// broken glass expects it to affect routing immediately. That shapes the
// design — scores live in an in-memory overlay that is replaced atomically,
// rather than in the graph itself.
package hazard

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
)

// Kinds a rider may report. These match the CHECK constraint on
// hazard_report.kind.
var Kinds = map[string]float64{
	"debris":             0.4,
	"pothole":            0.5,
	"poor_visibility":    0.6,
	"aggressive_traffic": 0.7,
	"blocked_lane":       0.8,
	"construction":       0.9,
	"dangerous_junction": 1.0,
	"other":              0.3,
}

// MatchRadiusM is how far a hazard's influence reaches.
//
// Wider than the crash radius because a rider dropping a pin from memory is
// less precise than a police report filed at a known intersection.
const MatchRadiusM = 30

// saturationConstant shapes the 1 - exp(-x/k) curve, as for crash pressure.
const saturationConstant = 1.5

// Report is a submitted hazard.
type Report struct {
	ID            int64      `json:"id"`
	Location      geo.LatLon `json:"location"`
	Kind          string     `json:"kind"`
	Notes         string     `json:"notes,omitempty"`
	Confirmations int        `json:"confirmations"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
}

// Index holds the current per-edge hazard scores.
//
// The scores are read on every edge evaluation inside the search loop, and
// written whenever a rider submits a report. Taking a lock per edge would
// dominate the search, so the whole slice is swapped atomically instead: a
// request takes one immutable snapshot at the start and reads it without
// synchronisation thereafter.
type Index struct {
	scores atomic.Pointer[[]float32]
}

func NewIndex(numEdges int) *Index {
	idx := &Index{}
	empty := make([]float32, numEdges)
	idx.scores.Store(&empty)
	return idx
}

// Snapshot returns the current scores. The slice must not be modified.
func (i *Index) Snapshot() []float32 {
	p := i.scores.Load()
	if p == nil {
		return nil
	}
	return *p
}

// replace swaps in a new score set.
func (i *Index) replace(s []float32) { i.scores.Store(&s) }

// Refresh recomputes every edge's hazard score from the database.
//
// This rebuilds the whole overlay rather than patching the edges near one new
// report. At 400k edges that is a few hundred milliseconds and happens only
// when someone submits a hazard, which is rare compared with routing; the
// simplicity is worth more than the cycles. If submissions ever become
// frequent, patch incrementally instead.
func (i *Index) Refresh(ctx context.Context, pool *pgxpool.Pool, g *graph.Graph) error {
	scores := make([]float32, g.NumEdges())

	// Four factors per hazard, mirroring crash pressure so the two signals
	// are comparable:
	//
	//   kind        — a blocked lane matters more than loose gravel;
	//   confirmation — corroboration by other riders raises confidence;
	//   freshness   — influence fades linearly toward the expiry date;
	//   distance    — attribution decays away from the reported point.
	//
	// Expired reports are excluded rather than deleted: the history is worth
	// keeping, and a lapsed hazard should simply stop steering routes.
	rows, err := pool.Query(ctx, `
		SELECT edge_id, sum(weight) AS total
		FROM (
			SELECT e.id AS edge_id,
			       coalesce((($1::jsonb) ->> h.kind)::double precision, 0.3)
			       * (1.0 + 0.25 * LEAST(h.confirmations, 8))
			       * GREATEST(0.0, EXTRACT(EPOCH FROM (h.expires_at - now()))
			                       / NULLIF(EXTRACT(EPOCH FROM (h.expires_at - h.created_at)), 0))
			       * exp(-ST_Distance(h.geom::geography, e.geom::geography) / 15.0)
			       AS weight
			FROM hazard_report h
			JOIN routing_edge e
			  ON e.geom && ST_Expand(h.geom, $2::double precision)
			 AND ST_DWithin(h.geom::geography, e.geom::geography, $3::double precision)
			WHERE h.expires_at > now()
		) matched
		GROUP BY edge_id`, kindWeightsJSON(), matchRadiusDeg, float64(MatchRadiusM))
	if err != nil {
		return fmt.Errorf("hazard: querying scores: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var edgeID int64
		var total float64
		if err := rows.Scan(&edgeID, &total); err != nil {
			return fmt.Errorf("hazard: scanning score: %w", err)
		}
		// The query returns routing_edge.id; the graph is indexed by
		// position after sorting. Translating is mandatory — indexing
		// directly by the database id lands the score on an unrelated road.
		idx, ok := g.IndexOfDBID(edgeID)
		if !ok {
			continue
		}
		scores[idx] = float32(saturate(total))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("hazard: reading scores: %w", err)
	}

	i.replace(scores)
	return nil
}

// matchRadiusDeg is MatchRadiusM in degrees, for the indexed bounding-box
// pre-filter. Writing the predicate directly against geography would bypass
// the GIST index on geom and scan every edge.
const matchRadiusDeg = 0.0005

// kindWeightsJSON renders the kind weights for the SQL aggregate, so the
// weights live in Go beside their documentation rather than being duplicated
// as a literal inside the query.
func kindWeightsJSON() string {
	b, err := json.Marshal(Kinds)
	if err != nil {
		return "{}" // unreachable for a map of string to float64
	}
	return string(b)
}

// saturate squashes an unbounded weight into 0..1 so that one heavily
// confirmed hazard cannot dominate the cost function.
func saturate(x float64) float64 {
	if x <= 0 {
		return 0
	}
	v := 1 - math.Exp(-x/saturationConstant)
	if v > 1 {
		return 1
	}
	return v
}
