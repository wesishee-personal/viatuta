package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/wesishee/viatuta/internal/hazard"
)

// handleReportHazard accepts a rider-submitted hazard.
//
// Reports are anonymous for now; the user_id column is nullable to match.
// Requiring an account arrives with authentication, at which point the
// submitter can be recorded and abuse becomes traceable.
func (s *Server) handleReportHazard(w http.ResponseWriter, r *http.Request) {
	var sub hazard.Submission
	if !decodeJSON(w, r, &sub) {
		return
	}
	if err := sub.Validate(); err != nil {
		details := map[string]string{"hazard": err.Error()}
		if errors.Is(err, hazard.ErrUnknownKind) {
			details["valid_kinds"] = strings.Join(kindNames(), ", ")
		}
		writeValidationError(w, "invalid hazard report", details)
		return
	}

	// Record the author when the caller is signed in; nil keeps the report
	// anonymous, which the nullable column allows.
	var userID *string
	if id := UserIDFrom(r.Context()); id != "" {
		userID = &id
	}

	rep, err := hazard.Create(r.Context(), s.pool, sub, userID)
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

// kindNames lists accepted hazard kinds for error messages.
func kindNames() []string {
	out := make([]string, 0, len(hazard.Kinds))
	for k := range hazard.Kinds {
		out = append(out, k)
	}
	// Sorted so the error message is stable between requests.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
