package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
	"github.com/wesishee/viatuta/internal/routing"
	"github.com/wesishee/viatuta/internal/safety"
)

// assumedSpeedKPH is a placeholder cycling speed for time estimates.
// Grade- and surface-aware timing is future work; a flat, conservative
// average is honest enough for now.
const assumedSpeedKPH = 15.0

// PlanRequest is the body of POST /v1/route/plan.
type PlanRequest struct {
	// Waypoints must contain at least an origin and a destination.
	Waypoints []geo.LatLon `json:"waypoints"`

	// Profile names a built-in rider profile: cautious, comfortable,
	// confident, or shortest. Defaults to comfortable.
	Profile string `json:"profile,omitempty"`

	// Custom overrides the named profile entirely. This is the
	// per-request tuning hook: any weight set, no redeploy.
	Custom *safety.Profile `json:"custom,omitempty"`

	// Night enables the lighting penalty.
	Night bool `json:"night,omitempty"`

	// Alternatives asks for up to this many additional, genuinely different
	// routes. Capped at 2 — beyond that the options stop being meaningfully
	// distinct and the extra searches are not free.
	Alternatives int `json:"alternatives,omitempty"`
}

// PlanResponse is a computed route.
type PlanResponse struct {
	Geometry GeoJSONGeometry `json:"geometry"`

	DistanceM float64 `json:"distance_m"`
	DurationS int     `json:"duration_s"`

	// Cost is the route's total in "effective meters": what the distance
	// would have to be if the whole route were protected track to feel
	// equally bad. Always at least DistanceM.
	Cost float64 `json:"cost"`

	// SafetyScore runs 0 (an unbroken arterial) to 100 (fully protected).
	SafetyScore float64 `json:"safety_score"`

	// Breakdown reports how far the route ran at each stress level and on
	// each facility type — the evidence behind SafetyScore.
	Breakdown safety.Breakdown `json:"breakdown"`

	// Segments say WHERE each of those distances happened, by indexing back
	// into Geometry. Breakdown alone tells a rider they have 400 m above
	// their comfort level without telling them which 400 m, which is the
	// part they need in order to decide whether to ride it.
	Segments []RouteSegment `json:"segments"`

	// StressBudget reports exposure above the rider's comfort threshold:
	// how much was permitted, and how much this route actually spends. The
	// rider sees the number rather than having to trust the promise.
	StressBudgetM     float64 `json:"stress_budget_m"`
	StressBudgetUsedM float64 `json:"stress_budget_used_m"`

	// Baseline is the shortest possible route, for comparison. The gap
	// between it and this route is precisely what the safety model bought
	// and what detour it charged for.
	BaselineDistanceM float64 `json:"baseline_distance_m"`
	BaselineScore     float64 `json:"baseline_safety_score"`
	DetourRatio       float64 `json:"detour_ratio"`

	Profile   safety.Profile `json:"profile"`
	Warnings  []string       `json:"warnings,omitempty"`
	Snapped   []SnapInfo     `json:"snapped"`
	EdgeCount int            `json:"edge_count"`
	ComputeMS int64          `json:"compute_ms"`

	// Alternatives are other routes worth considering, each meaningfully
	// different from the primary rather than a minor variation.
	Alternatives []AlternativeRoute `json:"alternatives,omitempty"`
}

// AlternativeRoute is a second-choice route with its trade-offs stated.
type AlternativeRoute struct {
	Geometry    GeoJSONGeometry  `json:"geometry"`
	DistanceM   float64          `json:"distance_m"`
	DurationS   int              `json:"duration_s"`
	Cost        float64          `json:"cost"`
	SafetyScore float64          `json:"safety_score"`
	Breakdown   safety.Breakdown `json:"breakdown"`

	// StressBudgetUsedM lets a rider see the exposure of each option side by
	// side, which is the whole point of offering a choice.
	StressBudgetUsedM float64 `json:"stress_budget_used_m"`
}

// GeoJSONGeometry is a GeoJSON LineString.
type GeoJSONGeometry struct {
	Type        string       `json:"type"`
	Coordinates [][2]float64 `json:"coordinates"`
}

// RouteSegment is a run of consecutive edges sharing the same traffic stress
// and facility type, indexed into Geometry.Coordinates.
//
// Runs are merged rather than emitted one per edge: a route is a few hundred
// edges but only a few dozen changes in character, and the merged form is
// what a client actually draws.
type RouteSegment struct {
	// Start and End index Geometry.Coordinates. End is INCLUSIVE, and is the
	// same coordinate as the next segment's Start — they share that junction,
	// so slicing [Start:End+1] yields polylines that join up.
	Start int `json:"start"`
	End   int `json:"end"`

	// LTS is 1..4, or 0 for an edge `viatuta score-edges` has not classified.
	LTS uint8 `json:"lts"`

	// Infra names the facility type, using the same vocabulary as
	// Breakdown.ByInfra.
	Infra string `json:"infra"`

	LengthM float64 `json:"length_m"`
}

// buildSegments turns per-edge attributes into drawable runs.
//
// offs comes from routing.FetchGeometry and has len(edges)+1 entries; see
// that function for the indexing convention. This mirrors the loop in
// safety.Summarise — same iteration over the same per-edge fields — but keeps
// where things happened rather than only how much.
func buildSegments(g *graph.Graph, edges []int32, offs []int32) []RouteSegment {
	if len(edges) == 0 || len(offs) != len(edges)+1 {
		return nil
	}

	var out []RouteSegment
	for i, e := range edges {
		start, end := int(offs[i]), int(offs[i+1])

		// An edge whose geometry was empty contributes no coordinates. It
		// still has length, so fold it into the run rather than emitting a
		// segment that draws nothing.
		degenerate := end <= start

		lts, infra := g.LTS[e], g.Infra[e].String()
		length := float64(g.LengthM[e])

		if n := len(out); n > 0 && (degenerate || (out[n-1].LTS == lts && out[n-1].Infra == infra)) {
			out[n-1].End = max(out[n-1].End, end)
			out[n-1].LengthM += length
			continue
		}
		if degenerate {
			continue
		}
		out = append(out, RouteSegment{
			Start:   start,
			End:     end,
			LTS:     lts,
			Infra:   infra,
			LengthM: length,
		})
	}

	for i := range out {
		out[i].LengthM = round1(out[i].LengthM)
	}
	return out
}

// SnapInfo describes how a requested point attached to the graph.
type SnapInfo struct {
	Requested geo.LatLon `json:"requested"`
	DistanceM float64    `json:"distance_m"`
	RoadName  string     `json:"road_name,omitempty"`
}

// handleRoutePlan computes a route through the requested waypoints.
func (s *Server) handleRoutePlan(w http.ResponseWriter, r *http.Request) {
	if s.graph == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "routing graph is not loaded yet")
		return
	}

	var req PlanRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	profile, ok := s.resolveProfile(w, req)
	if !ok {
		return
	}
	if !validWaypoints(w, req.Waypoints) {
		return
	}

	ctx := r.Context()
	started := time.Now()

	snaps := make([]graph.Snap, len(req.Waypoints))
	for i, p := range req.Waypoints {
		sn, err := s.snapper.Snap(ctx, p)
		if err != nil {
			if errors.Is(err, graph.ErrNoNearbyRoad) {
				writeError(w, http.StatusUnprocessableEntity, CodeUnroutable,
					"no rideable road near waypoint "+itoa(i))
				return
			}
			writeInternalError(w, s.logger, err)
			return
		}
		snaps[i] = sn
	}

	// One immutable hazard snapshot per request: the search loop then reads
	// it without locking, and the route cannot shift underneath itself if a
	// report lands mid-computation.
	var hazardScores []float32
	if s.hazards != nil {
		hazardScores = s.hazards.Snapshot()
	}

	model := safety.NewModel(profile).WithHazards(hazardScores)
	opts := routing.Options{
		EdgeCost: model.EdgeCost,
		TurnCost: model.TurnCost,
		Allow:    model.Allow,
		Over:     model.Over,
		BudgetM:  profile.StressBudgetM,
	}

	edges, cost, length, budgetUsed, err := s.routeLegs(snaps, opts)
	if err != nil {
		if errors.Is(err, routing.ErrUnroutable) {
			// Report the constraint that failed rather than silently
			// relaxing it. A rider who set a stress budget must never be
			// handed an arterial without being told.
			msg := "no route stays within this profile's stress budget of " +
				itoa(int(profile.StressBudgetM)) + " m above LTS " + itoa(int(profile.MaxLTS))
			if profile.StressBudgetM == 0 {
				msg = "no route satisfies this profile's maximum traffic stress of LTS " +
					itoa(int(profile.MaxLTS))
			}
			writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
				Code:    CodeUnroutable,
				Message: msg,
				Details: map[string]string{
					"max_lts":         itoa(int(profile.MaxLTS)),
					"stress_budget_m": itoa(int(profile.StressBudgetM)),
					"hint":            "raise stress_budget_m, or use a profile that tolerates more traffic stress",
				},
			})
			return
		}
		writeInternalError(w, s.logger, err)
		return
	}
	if len(edges) == 0 {
		writeError(w, http.StatusUnprocessableEntity, CodeUnroutable,
			"origin and destination resolve to the same point")
		return
	}

	// The distance-only baseline, for the comparison that makes the safety
	// route's cost legible.
	baseEdges, _, baseLength, _, _ := s.routeLegs(snaps, routing.Options{
		EdgeCost: routing.LengthOnlyCost,
	})

	pts, offs, err := routing.FetchGeometry(ctx, s.pool, s.graph, &routing.Path{Edges: edges})
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}
	coords := make([][2]float64, len(pts))
	for i, p := range pts {
		coords[i] = p.GeoJSON()
	}

	resp := PlanResponse{
		Geometry:    GeoJSONGeometry{Type: "LineString", Coordinates: coords},
		DistanceM:   round1(length),
		DurationS:   int(length / 1000 / assumedSpeedKPH * 3600),
		Cost:        round1(cost),
		SafetyScore: round1(safety.Score(s.graph, edges)),
		Breakdown:   model.Summarise(s.graph, edges),
		Segments:    buildSegments(s.graph, edges, offs),
		Profile:     profile,

		StressBudgetM:     profile.StressBudgetM,
		StressBudgetUsedM: round1(budgetUsed),
		EdgeCount:         len(edges),
		ComputeMS:         time.Since(started).Milliseconds(),
	}

	if baseLength > 0 {
		resp.BaselineDistanceM = round1(baseLength)
		resp.BaselineScore = round1(safety.Score(s.graph, baseEdges))
		resp.DetourRatio = round2(length / baseLength)

		if resp.DetourRatio > profile.MaxDetourRatio {
			resp.Warnings = append(resp.Warnings,
				"this route is "+ftoa(resp.DetourRatio)+"x the shortest distance, beyond this profile's limit of "+
					ftoa(profile.MaxDetourRatio)+"; a less cautious profile would be more direct")
		}
	}

	// Alternatives, when asked for. Only single-leg requests get them: with
	// via points the combinatorics multiply and the result stops being a
	// clear choice between two routes.
	if req.Alternatives > 0 && len(snaps) == 2 {
		if err := s.addAlternatives(ctx, &resp, snaps, opts, model, edges, req.Alternatives); err != nil {
			// A failure here costs the extras, not the route itself.
			s.logger.Warn("computing alternatives", "error", err,
				"request_id", RequestIDFrom(ctx))
		}
	}

	for i, sn := range snaps {
		resp.Snapped = append(resp.Snapped, SnapInfo{
			Requested: req.Waypoints[i],
			DistanceM: round1(sn.DistanceM),
			RoadName:  sn.RoadName,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// addAlternatives computes and attaches other routes worth considering.
//
// The primary route is recomputed as part of this, and discarded: running the
// alternatives search from scratch keeps the penalisation logic in one place
// rather than threading the already-computed path back into it. The extra
// search costs a few tens of milliseconds and only runs when asked for.
func (s *Server) addAlternatives(ctx context.Context, resp *PlanResponse,
	snaps []graph.Snap, opts routing.Options, model *safety.Model,
	primaryEdges []int32, want int) error {

	if want > 2 {
		want = 2
	}

	paths, err := routing.Alternatives(s.graph, snaps[0].NodeID, snaps[1].NodeID, opts, want)
	if err != nil {
		return err
	}

	for _, p := range paths[1:] { // skip the primary, already in the response
		pts, _, err := routing.FetchGeometry(ctx, s.pool, s.graph, p)
		if err != nil {
			return err
		}
		coords := make([][2]float64, len(pts))
		for i, pt := range pts {
			coords[i] = pt.GeoJSON()
		}

		resp.Alternatives = append(resp.Alternatives, AlternativeRoute{
			Geometry:          GeoJSONGeometry{Type: "LineString", Coordinates: coords},
			DistanceM:         round1(p.LengthM),
			DurationS:         int(p.LengthM / 1000 / assumedSpeedKPH * 3600),
			Cost:              round1(p.Cost),
			SafetyScore:       round1(safety.Score(s.graph, p.Edges)),
			Breakdown:         model.Summarise(s.graph, p.Edges),
			StressBudgetUsedM: round1(p.BudgetUsedM),
		})
	}
	return nil
}

// routeLegs routes each consecutive waypoint pair and concatenates the result.
//
// Note that the stress budget applies per leg, not across the whole journey.
// Each leg is an independent search, so a three-waypoint trip may spend the
// budget twice. That is the honest reading of a via point as "take me through
// here": the rider chose the intermediate stop, so each stage is its own trip.
func (s *Server) routeLegs(snaps []graph.Snap, opts routing.Options) ([]int32, float64, float64, float64, error) {
	var all []int32
	var cost, length, budgetUsed float64

	for i := 0; i+1 < len(snaps); i++ {
		leg, err := routing.Search(s.graph, snaps[i].NodeID, snaps[i+1].NodeID, opts)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		all = append(all, leg.Edges...)
		cost += leg.Cost
		length += leg.LengthM
		budgetUsed += leg.BudgetUsedM
	}
	return all, cost, length, budgetUsed, nil
}

// resolveProfile picks the profile for this request, validating a custom one.
func (s *Server) resolveProfile(w http.ResponseWriter, req PlanRequest) (safety.Profile, bool) {
	if req.Custom != nil {
		p := *req.Custom
		if p.Name == "" {
			p.Name = "custom"
		}
		if err := p.Validate(); err != nil {
			writeValidationError(w, "invalid custom profile", map[string]string{"custom": err.Error()})
			return safety.Profile{}, false
		}
		p.Night = p.Night || req.Night
		return p, true
	}

	p, err := safety.ProfileByName(req.Profile)
	if err != nil {
		writeValidationError(w, "unknown profile", map[string]string{"profile": err.Error()})
		return safety.Profile{}, false
	}
	p.Night = req.Night
	return p, true
}

func validWaypoints(w http.ResponseWriter, pts []geo.LatLon) bool {
	if len(pts) < 2 {
		writeValidationError(w, "at least two waypoints are required",
			map[string]string{"waypoints": "provide an origin and a destination"})
		return false
	}
	if len(pts) > 10 {
		writeValidationError(w, "too many waypoints",
			map[string]string{"waypoints": "at most 10 are supported"})
		return false
	}
	for i, p := range pts {
		if !p.Valid() {
			writeValidationError(w, "invalid coordinate",
				map[string]string{"waypoints": "point " + itoa(i) + " is out of range"})
			return false
		}
	}
	return true
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// ftoa formats a ratio to two decimals for use in messages.
func ftoa(v float64) string {
	whole := int(v)
	frac := int((v-float64(whole))*100 + 0.5)
	return itoa(whole) + "." + pad2(frac)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// itoa avoids pulling strconv in for a few small uses.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
