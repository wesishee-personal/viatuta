package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
	"github.com/wesishee/viatuta/internal/routing"
	"github.com/wesishee/viatuta/internal/safety"
	"github.com/wesishee/viatuta/internal/savedroute"
)

// SaveRouteRequest is the body of POST /v1/routes.
//
// It carries the same inputs as a plan request plus a name. The route is
// recomputed server-side rather than accepting client-supplied geometry:
// otherwise a caller could store any path at all and have it come back
// labelled with a safety score this service never produced.
type SaveRouteRequest struct {
	Name      string          `json:"name"`
	Waypoints []geo.LatLon    `json:"waypoints"`
	Profile   string          `json:"profile,omitempty"`
	Custom    *safety.Profile `json:"custom,omitempty"`
	Night     bool            `json:"night,omitempty"`
}

func (s *Server) handleSaveRoute(w http.ResponseWriter, r *http.Request) {
	if s.graph == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "routing graph is not loaded yet")
		return
	}

	var req SaveRouteRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name, err := savedroute.ValidateName(req.Name)
	if err != nil {
		writeValidationError(w, "invalid route name", map[string]string{"name": err.Error()})
		return
	}
	profile, ok := s.resolveProfile(w, PlanRequest{Profile: req.Profile, Custom: req.Custom, Night: req.Night})
	if !ok {
		return
	}
	if !validWaypoints(w, req.Waypoints) {
		return
	}

	ctx := r.Context()

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

	var hazardScores []float32
	if s.hazards != nil {
		hazardScores = s.hazards.Snapshot()
	}
	model := safety.NewModel(profile).WithHazards(hazardScores)

	edges, _, length, _, err := s.routeLegs(snaps, routing.Options{
		EdgeCost: model.EdgeCost,
		TurnCost: model.TurnCost,
		Allow:    model.Allow,
		Over:     model.Over,
		BudgetM:  profile.StressBudgetM,
	})
	if err != nil {
		if errors.Is(err, routing.ErrUnroutable) {
			writeError(w, http.StatusUnprocessableEntity, CodeUnroutable,
				"no route satisfies this profile; nothing was saved")
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

	pts, _, err := routing.FetchGeometry(ctx, s.pool, s.graph, &routing.Path{Edges: edges})
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}

	saved, err := savedroute.Create(ctx, s.pool, UserIDFrom(ctx), savedroute.Input{
		Name:        name,
		Waypoints:   req.Waypoints,
		Geometry:    pts,
		DistanceM:   round1(length),
		DurationS:   int(length / 1000 / assumedSpeedKPH * 3600),
		SafetyScore: round1(safety.Score(s.graph, edges)),
		Profile:     profile,
	})
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	routes, err := savedroute.List(r.Context(), s.pool, UserIDFrom(r.Context()), limit)
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": routes, "count": len(routes)})
}

func (s *Server) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	route, err := savedroute.Get(r.Context(), s.pool, UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, savedroute.ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
			return
		}
		// A malformed uuid also lands here; it is equally "not found" from
		// the caller's point of view.
		writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
		return
	}

	// Return the stored geometry in GeoJSON form, matching /v1/route/plan so
	// a client can render either with the same code.
	coords := make([][2]float64, len(route.Geometry))
	for i, p := range route.Geometry {
		coords[i] = p.GeoJSON()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           route.ID,
		"name":         route.Name,
		"waypoints":    route.Waypoints,
		"distance_m":   route.DistanceM,
		"duration_s":   route.DurationS,
		"safety_score": route.SafetyScore,
		"profile":      route.Profile,
		"created_at":   route.CreatedAt,
		"geometry":     GeoJSONGeometry{Type: "LineString", Coordinates: coords},
	})
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	err := savedroute.Delete(r.Context(), s.pool, UserIDFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, savedroute.ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
			return
		}
		writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
