package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wesishee/viatuta/internal/auth"
	"github.com/wesishee/viatuta/internal/config"
)

const testSecret = "a-test-signing-secret-long-enough"

func authServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(config.Config{Env: "test", JWTSecret: testSecret}, logger, nil, nil)
	if s.signer == nil {
		t.Fatal("signer was not configured")
	}
	return s
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name, header, want string
		ok                 bool
	}{
		{"standard", "Bearer abc123", "abc123", true},
		// RFC 7235 says the scheme is case-insensitive.
		{"lowercase scheme", "bearer abc123", "abc123", true},
		{"mixed case scheme", "BeArEr abc123", "abc123", true},
		{"surrounding spaces", "Bearer   abc123  ", "abc123", true},
		{"absent", "", "", false},
		{"wrong scheme", "Basic abc123", "", false},
		{"scheme only", "Bearer", "", false},
		{"empty token", "Bearer   ", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			got, ok := bearerToken(r)
			if ok != tc.ok || got != tc.want {
				t.Errorf("bearerToken(%q) = (%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// probe is a handler that records the user id the middleware attached.
func probe(seen *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*seen = UserIDFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}
}

func TestRequireAuthRejectsUnauthenticated(t *testing.T) {
	s := authServer(t)
	var seen string

	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"garbage", "Bearer not-a-token"},
		{"wrong scheme", "Basic dXNlcjpwYXNz"},
		{"empty bearer", "Bearer "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			s.requireAuth(probe(&seen))(rec, r)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			// RFC 6750: a 401 must say how to authenticate.
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("missing WWW-Authenticate header")
			}
		})
	}
}

func TestRequireAuthAcceptsValidToken(t *testing.T) {
	s := authServer(t)
	signer, err := auth.NewSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := signer.Issue("user-abc", "wes@example.com")
	if err != nil {
		t.Fatal(err)
	}

	var seen string
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.requireAuth(probe(&seen))(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if seen != "user-abc" {
		t.Errorf("handler saw user %q, want user-abc", seen)
	}
}

// TestRequireAuthRejectsForeignToken checks that a token signed with a
// different secret cannot authenticate.
func TestRequireAuthRejectsForeignToken(t *testing.T) {
	s := authServer(t)
	other, err := auth.NewSigner("a-completely-different-secret-here")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := other.Issue("attacker", "mallory@example.com")
	if err != nil {
		t.Fatal(err)
	}

	var seen string
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.requireAuth(probe(&seen))(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if seen != "" {
		t.Errorf("handler ran with user %q; it should not have run at all", seen)
	}
}

// TestOptionalAuthAllowsAnonymous checks the hazard-reporting path: no token
// is fine, and a bad token is treated as no token rather than as a failure.
func TestOptionalAuthAllowsAnonymous(t *testing.T) {
	s := authServer(t)

	for _, header := range []string{"", "Bearer nonsense"} {
		var seen string
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		s.optionalAuth(probe(&seen))(rec, r)

		if rec.Code != http.StatusOK {
			t.Errorf("header %q: status = %d, want 200", header, rec.Code)
		}
		if seen != "" {
			t.Errorf("header %q: user = %q, want anonymous", header, seen)
		}
	}
}

func TestOptionalAuthAttachesUserWhenPresent(t *testing.T) {
	s := authServer(t)
	signer, _ := auth.NewSigner(testSecret)
	token, _, _ := signer.Issue("user-xyz", "wes@example.com")

	var seen string
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.optionalAuth(probe(&seen))(rec, r)

	if seen != "user-xyz" {
		t.Errorf("user = %q, want user-xyz", seen)
	}
}

// TestAccountEndpointsDisabledWithoutSecret checks the fail-closed behaviour:
// with no signing secret the server refuses account operations rather than
// signing tokens with something guessable.
func TestAccountEndpointsDisabledWithoutSecret(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(config.Config{Env: "test"}, logger, nil, nil)
	if s.signer != nil {
		t.Fatal("a signer was built without a secret")
	}

	var seen string
	rec := httptest.NewRecorder()
	s.requireAuth(probe(&seen))(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
