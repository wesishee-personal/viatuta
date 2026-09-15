package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/wesishee/viatuta/internal/auth"
)

// RegisterRequest is the body of POST /v1/auth/register.
type RegisterRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
}

// LoginRequest is the body of POST /v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AuthResponse carries a token and the account it belongs to.
type AuthResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      auth.User `json:"user"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"authentication is not configured on this server")
		return
	}

	var req RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := auth.Register(r.Context(), s.pool, req.Email, req.Password, req.DisplayName)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrEmailTaken):
			writeError(w, http.StatusConflict, CodeConflict, err.Error())
		case errors.Is(err, auth.ErrWeakPassword):
			writeValidationError(w, "password does not meet requirements",
				map[string]string{"password": err.Error()})
		case errors.Is(err, auth.ErrInvalidEmail):
			writeValidationError(w, "invalid registration",
				map[string]string{"email": err.Error()})
		default:
			writeInternalError(w, s.logger, err)
		}
		return
	}

	s.issueToken(w, user, http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"authentication is not configured on this server")
		return
	}

	var req LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := auth.Authenticate(r.Context(), s.pool, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			// One message for both an unknown address and a wrong password,
			// so the response cannot be used to discover who has an account.
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid email or password")
			return
		}
		writeInternalError(w, s.logger, err)
		return
	}

	s.issueToken(w, user, http.StatusOK)
}

// handleMe returns the authenticated account.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := auth.ByID(r.Context(), s.pool, UserIDFrom(r.Context()))
	if err != nil {
		// A valid token for a deleted account.
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "account no longer exists")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// issueToken mints a token and writes the response.
func (s *Server) issueToken(w http.ResponseWriter, user auth.User, status int) {
	token, expires, err := s.signer.Issue(user.ID, user.Email)
	if err != nil {
		writeInternalError(w, s.logger, err)
		return
	}
	writeJSON(w, status, AuthResponse{Token: token, ExpiresAt: expires, User: user})
}
