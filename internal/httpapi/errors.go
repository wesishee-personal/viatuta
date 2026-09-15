package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ErrorResponse is the single shape every failure returns.
//
// One consistent envelope means a client can always parse a failure the same
// way. `Code` is a stable machine-readable string; `Message` is for humans
// and may change wording freely.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details carries field-level validation problems, e.g.
	// {"waypoints": "at least two required"}.
	Details map[string]string `json:"details,omitempty"`
}

// Common error codes. Keep these stable — clients branch on them.
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeInternal     = "internal_error"
	CodeUnroutable   = "unroutable" // no safe path exists under the given profile
)

// writeJSON serializes v as the response body with the given status code.
//
// The Content-Type must be set before WriteHeader; once WriteHeader is
// called the headers are flushed and further changes are silently ignored.
// This ordering bug is common enough to be worth centralizing here.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so we cannot turn this into a
		// 500 — all we can do is record it.
		slog.Error("writing json response", "error", err)
	}
}

// writeError sends a structured error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}

// writeValidationError sends a 400 with per-field details.
func writeValidationError(w http.ResponseWriter, message string, details map[string]string) {
	writeJSON(w, http.StatusBadRequest, ErrorResponse{
		Code:    CodeBadRequest,
		Message: message,
		Details: details,
	})
}

// writeInternalError logs the real cause and returns a deliberately vague
// message to the client.
//
// Internal errors often contain table names, file paths, or query fragments.
// Those help an attacker and mean nothing to a legitimate user, so the detail
// goes to the log and only a generic message goes over the wire.
func writeInternalError(w http.ResponseWriter, logger *slog.Logger, err error) {
	logger.Error("internal error", "error", err)
	writeError(w, http.StatusInternalServerError, CodeInternal, "an internal error occurred")
}

// decodeJSON reads and validates a JSON request body into dst.
//
// It rejects unknown fields so that a typo in a client's request — say
// "waypoint" instead of "waypoints" — is an immediate, obvious error rather
// than a silently ignored field and a confusing downstream failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// Cap the body size so a malicious or buggy client cannot exhaust
	// memory by streaming an endless request.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
