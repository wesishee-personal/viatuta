package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHazardWritesRequireAuth checks the wiring in Handler(), not just the
// middleware: a hazard write without a valid token must be rejected before it
// can reach the database.
func TestHazardWritesRequireAuth(t *testing.T) {
	h := authServer(t).Handler()

	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/v1/hazards", `{"location":{"lat":30.27,"lon":-97.74},"kind":"pothole"}`},
		{http.MethodPost, "/v1/hazards/1/confirm", ``},
		{http.MethodPost, "/v1/hazards/1/resolve", ``},
	}
	headers := map[string]string{
		"no token":      "",
		"garbage token": "Bearer nonsense",
	}

	for _, rt := range routes {
		for name, header := range headers {
			t.Run(rt.method+" "+rt.path+" "+name, func(t *testing.T) {
				r := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
				if header != "" {
					r.Header.Set("Authorization", header)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)

				if rec.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401; body: %s", rec.Code, rec.Body)
				}
			})
		}
	}
}
