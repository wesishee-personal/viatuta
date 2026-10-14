package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/wesishee/viatuta/internal/hazard"
)

// handleReportHazard accepts a rider-submitted hazard. The route is behind
// requireAuth, so the author is always known.
func (s *Server) handleReportHazard(w http.ResponseWriter, r *http.Request) {
	var sub hazard.Submission
	if !decodeJSON(w, r, &sub) {
		return
	}
	if err := sub.Validate(); err != nil {
		details := map[string]string{"hazard": err.Error()}
		if errors.Is(err, hazard.ErrUnknownKind) {
			details["valid_kinds"] = strings.Join(hazard.Names(), ", ")
		}
		writeValidationError(w, "invalid hazard report", details)
		return
	}

	rep, err := hazard.Create(r.Context(), s.pool, sub, UserIDFrom(r.Context()))
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}

	// Rebuild the routing overlay so the next request already avoids this.
	// A failure here is logged rather than returned: the report IS stored,
	// and telling the rider it failed would invite a duplicate submission.
	if s.hazards != nil && s.graph != nil {
		if err := s.hazards.Refresh(r.Context(), s.pool, s.graph); err != nil {
			s.logger.Error("refreshing hazard overlay", "error", err, "hazard_id", rep.ID)
		}
	}

	writeJSON(w, http.StatusCreated, rep)
}

// handleListHazards returns active hazards in a bounding box.
func (s *Server) handleListHazards(w http.ResponseWriter, r *http.Request) {
	bbox := r.URL.Query().Get("bbox")
	if bbox == "" {
		writeValidationError(w, "bbox is required",
			map[string]string{"bbox": "minLon,minLat,maxLon,maxLat"})
		return
	}

	parts := strings.Split(bbox, ",")
	if len(parts) != 4 {
		writeValidationError(w, "malformed bbox",
			map[string]string{"bbox": "expected four comma-separated numbers: minLon,minLat,maxLon,maxLat"})
		return
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			writeValidationError(w, "malformed bbox",
				map[string]string{"bbox": "value " + itoa(i) + " is not a number"})
			return
		}
		v[i] = f
	}
	if v[0] > v[2] || v[1] > v[3] {
		writeValidationError(w, "malformed bbox",
			map[string]string{"bbox": "minimum values must not exceed maximum values"})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	reports, err := hazard.ListInBBox(r.Context(), s.pool, v[0], v[1], v[2], v[3], limit)
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hazards": reports,
		"count":   len(reports),
	})
}

// handleConfirmHazard records corroboration from another rider.
func (s *Server) handleConfirmHazard(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeValidationError(w, "invalid hazard id", map[string]string{"id": "must be a number"})
		return
	}

	rep, err := hazard.Confirm(r.Context(), s.pool, id)
	if err != nil {
		// The only expected failure is an id that is gone or expired.
		writeError(w, http.StatusNotFound, CodeNotFound, "no active hazard with that id")
		return
	}

	if s.hazards != nil && s.graph != nil {
		if err := s.hazards.Refresh(r.Context(), s.pool, s.graph); err != nil {
			s.logger.Error("refreshing hazard overlay", "error", err, "hazard_id", rep.ID)
		}
	}

	writeJSON(w, http.StatusOK, rep)
}

// KindInfo describes a hazard kind to clients.
type KindInfo struct {
	Kind           string  `json:"kind"`
	Label          string  `json:"label"`
	TTLSeconds     int64   `json:"ttl_s"`
	ConfirmExtendS int64   `json:"confirm_extension_s"`
	MaxLifeSeconds int64   `json:"max_life_s"`
	MergeRadiusM   float64 `json:"merge_radius_m"`
	Directional    bool    `json:"directional"`
}

// handleListKinds returns every hazard kind with its lifetime, so clients
// need not hard-code the table.
func (s *Server) handleListKinds(w http.ResponseWriter, r *http.Request) {
	all := hazard.All()
	out := make([]KindInfo, len(all))
	for i, k := range all {
		out[i] = KindInfo{
			Kind:           k.Name,
			Label:          k.Label,
			TTLSeconds:     int64(k.TTL.Seconds()),
			ConfirmExtendS: int64(k.ConfirmExtension.Seconds()),
			MaxLifeSeconds: int64(k.MaxLife.Seconds()),
			MergeRadiusM:   k.MergeRadiusM,
			Directional:    k.Directional,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": out})
}
