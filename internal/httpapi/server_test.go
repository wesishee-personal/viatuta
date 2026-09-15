package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wesishee/viatuta/internal/config"
)

// testServer builds a Server with no database.
//
// /healthz must not touch the database — that is the whole distinction
// between liveness and readiness — so a nil pool here is not a shortcut,
// it actively enforces the property.
func testServer() *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(config.Config{Env: "test"}, logger, nil, nil)
}

func TestHealthz(t *testing.T) {
	// httptest.NewRecorder captures what a handler writes, so we can assert
	// on a real response without opening a socket.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	testServer().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want %q", got.Status, "ok")
	}
	if got.Uptime == "" {
		t.Error("uptime is empty")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestRequestIDIsEchoed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header missing")
	}
}

func TestRequestIDIsPreservedWhenSupplied(t *testing.T) {
	// A caller-supplied ID must survive, so a trace can span services.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "caller-supplied-id")
	rec := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "caller-supplied-id" {
		t.Errorf("X-Request-ID = %q, want %q", got, "caller-supplied-id")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	// Go 1.22 routing knows /healthz exists but only for GET, so a POST
	// must be 405 rather than 404.
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/v1/route/plan", nil)
	rec := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("missing CORS origin header")
	}
}

// TestPanicIsRecovered proves a handler panic becomes a 500 rather than
// taking down the process.
func TestPanicIsRecovered(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	boom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	h := chain(boom, withRequestID, withRecover(logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var got ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Code != CodeInternal {
		t.Errorf("code = %q, want %q", got.Code, CodeInternal)
	}
	// The panic value must not leak to the client.
	if body := rec.Body.String(); strings.Contains(body, "boom") {
		t.Errorf("panic detail leaked into response: %s", body)
	}
}
