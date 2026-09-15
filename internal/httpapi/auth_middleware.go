package httpapi

import (
	"context"
	"net/http"
	"strings"
)

const userIDKey contextKey = "user_id"
const userEmailKey contextKey = "user_email"

// UserIDFrom returns the authenticated user's id, or "" when the request is
// anonymous.
func UserIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// requireAuth rejects requests without a valid bearer token.
//
// It wraps a HandlerFunc rather than a Handler so route declarations read
// naturally: mux.HandleFunc("POST /v1/routes", s.requireAuth(s.handleSave)).
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.signer == nil {
			writeError(w, http.StatusServiceUnavailable, CodeInternal,
				"authentication is not configured on this server")
			return
		}

		token, ok := bearerToken(r)
		if !ok {
			// RFC 6750 says a 401 should say how to authenticate.
			w.Header().Set("WWW-Authenticate", `Bearer realm="viatuta"`)
			writeError(w, http.StatusUnauthorized, CodeUnauthorized,
				"a bearer token is required")
			return
		}

		claims, err := s.signer.Verify(token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="viatuta", error="invalid_token"`)
			// The specific reason — expired, forged, wrong issuer — goes to
			// the log only. Telling a caller which part failed helps an
			// attacker refine a forgery and helps a legitimate user not at
			// all.
			s.logger.Warn("rejected token", "error", err, "request_id", RequestIDFrom(r.Context()))
			writeError(w, http.StatusUnauthorized, CodeUnauthorized,
				"invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, claims.UserID())
		ctx = context.WithValue(ctx, userEmailKey, claims.Email)
		next(w, r.WithContext(ctx))
	}
}

// optionalAuth attaches the user when a valid token is present but allows the
// request through when it is not.
//
// Used for endpoints that work anonymously but record the author when known,
// such as hazard reports.
func (s *Server) optionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.signer == nil {
			next(w, r)
			return
		}
		token, ok := bearerToken(r)
		if !ok {
			next(w, r)
			return
		}
		claims, err := s.signer.Verify(token)
		if err != nil {
			// A bad token on an optional route is treated as no token. The
			// alternative — failing the request — would make a stale token
			// worse than none at all.
			next(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, claims.UserID())
		ctx = context.WithValue(ctx, userEmailKey, claims.Email)
		next(w, r.WithContext(ctx))
	}
}

// bearerToken extracts a token from the Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	// The scheme is case-insensitive per RFC 7235.
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}
