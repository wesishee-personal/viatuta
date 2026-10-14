// Package hazard handles rider-submitted road hazards.
//
// Hazards differ from every other signal in this system: they are live. Collision
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

// MatchRadiusM is how far a hazard's influence reaches.
//
// Wider than the collision radius because a rider dropping a pin from memory is
// less precise than a police report filed at a known intersection.
const MatchRadiusM = 30

// saturationConstant shapes the 1 - exp(-x/k) curve, as for collision pressure.
const saturationConstant = 1.5

// Report is a submitted hazard.
type Report struct {
	ID       int64      `json:"id"`
	Location geo.LatLon `json:"location"`
	Kind     string     `json:"kind"`
	Notes    string     `json:"notes,omitempty"`
	// HeadingDeg is the direction of travel the hazard affects, in degrees
	// clockwise from north. Absent means both directions.
	HeadingDeg    *int      `json:"heading_deg,omitempty"`
	Confirmations int       `json:"confirmations"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Snapshot is an immutable view of the hazard overlay, indexed by graph edge.
type Snapshot struct {
	// Scores are ordinary hazard scores, 0..1 per edge.
	Scores []float32

	// Closures are extra cost multipliers for edges reported closed, 0 where
	// there is no closure. Unlike Scores they are not a 0..1 "how bad" value:
	// they are added straight to the edge's cost multiplier. See ClosurePenalty.
	Closures []float32
}

// Index holds the current per-edge hazard overlay.
//
// The overlay is read on every edge evaluation inside the search loop, and
// written whenever the set of hazards changes. Taking a lock per edge would
// dominate the search, so the whole snapshot is swapped atomically instead: a
// request takes one immutable snapshot at the start and reads it without
// synchronisation thereafter.
type Index struct {
	snap atomic.Pointer[Snapshot]
}

func NewIndex(numEdges int) *Index {
	idx := &Index{}
	idx.snap.Store(&Snapshot{
		Scores:   make([]float32, numEdges),
		Closures: make([]float32, numEdges),
	})
	return idx
}

// Snapshot returns the current overlay. The slices must not be modified.
func (i *Index) Snapshot() Snapshot {
	if p := i.snap.Load(); p != nil {
		return *p
	}
	return Snapshot{}
}

// replace swaps in a new overlay.
func (i *Index) replace(s Snapshot) { i.snap.Store(&s) }

// Refresh recomputes every edge's hazard score from the database.
//
// This rebuilds the whole overlay rather than patching the edges near one new
// report. At 400k edges that is a few hundred milliseconds and happens only
// when someone submits a hazard, which is rare compared with routing; the
// simplicity is worth more than the cycles. If submissions ever become
// frequent, patch incrementally instead.
func (i *Index) Refresh(ctx context.Context, pool *pgxpool.Pool, g *graph.Graph) error {
	scores := make([]float32, g.NumEdges())
	closures := make([]float32, g.NumEdges())

	// Four factors per hazard, mirroring collision pressure so the two signals
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
			 -- Edges are directed, so a directional hazard can be applied to the
			 -- lane it is in and not to the opposite one. The edge's bearing is
			 -- read at whichever end is nearer the hazard, because roads curve.
			 -- An edge with no bearing, or a hazard with no heading, matches.
			 AND (h.heading_deg IS NULL
			      OR NOT (h.kind = ANY($4::text[]))
			      OR e.bearing_start IS NULL
			      OR abs(((CASE WHEN ST_LineLocatePoint(e.geom, h.geom) < 0.5
			                    THEN e.bearing_start ELSE e.bearing_end END)::int
			               - h.heading_deg + 540) % 360 - 180) <= 90)
			WHERE h.expires_at > now()
			  AND NOT (h.kind = ANY($5::text[]))
		) matched
		GROUP BY edge_id`, kindWeightsJSON(), matchRadiusDeg, float64(MatchRadiusM), directionalKinds(), closureKinds())
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

	if err := refreshClosures(ctx, pool, g, closures); err != nil {
		return err
	}

	i.replace(Snapshot{Scores: scores, Closures: closures})
	return nil
}

// refreshClosures fills closures[edge] for every edge near an active closure.
//
// The penalty depends on how many riders reported the closure (the original
// report plus each confirmation), taking the largest where several closures
// reach the same edge. There is deliberately no fade with age and no distance
// decay: a closure is as closed at hour twenty as at hour one, and it comes
// down by expiring or being resolved, not by weakening.
func refreshClosures(ctx context.Context, pool *pgxpool.Pool, g *graph.Graph, closures []float32) error {
	rows, err := pool.Query(ctx, `
		SELECT e.id, max(1 + h.confirmations)
		FROM hazard_report h
		JOIN routing_edge e
		  ON e.geom && ST_Expand(h.geom, $2::double precision)
		 AND ST_DWithin(h.geom::geography, e.geom::geography, $3::double precision)
		WHERE h.expires_at > now()
		  AND h.kind = ANY($1::text[])
		GROUP BY e.id`, closureKinds(), matchRadiusDeg, float64(ClosureRadiusM))
	if err != nil {
		return fmt.Errorf("hazard: querying closures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var edgeID int64
		var reporters int
		if err := rows.Scan(&edgeID, &reporters); err != nil {
			return fmt.Errorf("hazard: scanning closure: %w", err)
		}
		// As above: translate the database id to a graph position.
		if idx, ok := g.IndexOfDBID(edgeID); ok {
			closures[idx] = float32(ClosurePenalty(reporters))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("hazard: reading closures: %w", err)
	}
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
	weights := make(map[string]float64, len(kinds))
	for _, k := range kinds {
		weights[k.Name] = k.Weight
	}
	b, err := json.Marshal(weights)
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

// MaxHeadingDiffDeg is how far an edge's direction may differ from a hazard's
// heading and still be affected by it. Ninety degrees is "roughly the same
// way": it includes a road that bends a little and excludes the opposite
// carriageway. The overlay query applies the same rule in SQL.
const MaxHeadingDiffDeg = 90

// AngleDiff returns the smallest angle between two compass bearings, in
// degrees, 0..180. Bearings wrap, so 350 and 10 are 20 apart, not 340.
func AngleDiff(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// directionalKinds lists the kinds whose heading steers routing.
func directionalKinds() []string {
	var out []string
	for _, k := range kinds {
		if k.Directional {
			out = append(out, k.Name)
		}
	}
	return out
}

// closureKinds lists the kinds scored as closures.
func closureKinds() []string {
	var out []string
	for _, k := range kinds {
		if k.Closure {
			out = append(out, k.Name)
		}
	}
	return out
}

// Closure penalty parameters. These are starting values to tune against real
// routes, not measurements.
const (
	// ClosureRadiusM is how far from the reported point a road counts as
	// closed. Tighter than MatchRadiusM: a closure is a claim about a road,
	// and a wide radius would close the crossing streets at a junction too.
	ClosureRadiusM = 20

	closureBase     = 15.0
	closureExponent = 1.5
	closureCap      = 150.0
)

// ClosurePenalty is the extra cost multiplier for a road reported closed by
// `reporters` riders: one report makes the road about 16 times as expensive,
// three make it about 79 times, and it levels off at 151 times.
//
// It is aggressive and it is finite, on purpose. A closure report is
// unverified, so the router must be able to use the road if the report is
// wrong or it is the only way through. Many independent reports make that
// progressively less likely to be worth it.
func ClosurePenalty(reporters int) float64 {
	if reporters <= 0 {
		return 0
	}
	return math.Min(closureCap, closureBase*math.Pow(float64(reporters), closureExponent))
}
