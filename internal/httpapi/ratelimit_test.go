package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wesishee/viatuta/internal/auth"
)

// fakeClock lets limiter tests move time by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func limiterWithClock(perHour, burst int) (*userLimiters, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	l := newUserLimiters(perHour, burst)
	l.now = clock.now
	return l, clock
}

func TestLimiterAllowsABurstThenRefuses(t *testing.T) {
	l, _ := limiterWithClock(20, 5)

	for i := 1; i <= 5; i++ {
		if ok, _ := l.allow("u1"); !ok {
			t.Fatalf("request %d of the burst was refused", i)
		}
	}
	ok, wait := l.allow("u1")
	if ok {
		t.Fatal("the sixth request should be refused")
	}
	// 20 an hour is one token every 3 minutes.
	if wait <= 0 || wait > 3*time.Minute {
		t.Errorf("wait = %v, want between 0 and 3m", wait)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l, clock := limiterWithClock(20, 5)
	for i := 0; i < 5; i++ {
		l.allow("u1")
	}
	if ok, _ := l.allow("u1"); ok {
		t.Fatal("bucket should be empty")
	}

	clock.advance(3*time.Minute + time.Second) // one token
	if ok, _ := l.allow("u1"); !ok {
		t.Error("a token should have refilled after 3 minutes")
	}
	if ok, _ := l.allow("u1"); ok {
		t.Error("only one token should have refilled")
	}
}

// A refused request must not spend allowance, or a client retrying in a loop
// would push its own recovery further away.
func TestRefusedRequestsDoNotDelayRecovery(t *testing.T) {
	l, clock := limiterWithClock(20, 5)
	for i := 0; i < 5; i++ {
		l.allow("u1")
	}
	for i := 0; i < 50; i++ { // hammering while limited
		l.allow("u1")
	}
	clock.advance(3*time.Minute + time.Second)
	if ok, _ := l.allow("u1"); !ok {
		t.Error("hammering while limited delayed recovery")
	}
}

func TestLimitsArePerUser(t *testing.T) {
	l, _ := limiterWithClock(20, 5)
	for i := 0; i < 5; i++ {
		l.allow("busy")
	}
	if ok, _ := l.allow("busy"); ok {
		t.Fatal("busy user should be limited")
	}
	if ok, _ := l.allow("quiet"); !ok {
		t.Error("one user's usage limited another")
	}
}

func TestIdleLimitersAreEvicted(t *testing.T) {
	l, clock := limiterWithClock(20, 5)
	for _, id := range []string{"a", "b", "c"} {
		l.allow(id)
	}
	if len(l.byUser) != 3 {
		t.Fatalf("tracking %d users, want 3", len(l.byUser))
	}

	clock.advance(limiterIdleTTL + time.Minute)
	l.allow("d") // any call triggers the sweep
	if len(l.byUser) != 1 {
		t.Errorf("tracking %d users after the idle period, want 1 (only d)", len(l.byUser))
	}
}

func authedRequest(t *testing.T, s *Server, userID, method, path, body string) *http.Request {
	t.Helper()
	signer, err := auth.NewSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := signer.Issue(userID, userID+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestRateLimitedResponseShape(t *testing.T) {
	s := authServer(t)
	l, _ := limiterWithClock(20, 1)
	h := s.requireAuth(s.rateLimited(l, "widgets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h(rec, authedRequest(t, s, "u1", http.MethodPost, "/", ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d, want 204", rec.Code)
	}

	rec = httptest.NewRecorder()
	h(rec, authedRequest(t, s, "u1", http.MethodPost, "/", ""))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec.Code)
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 3*60 {
		t.Errorf("Retry-After = %q, want whole seconds up to 180", rec.Header().Get("Retry-After"))
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != CodeRateLimited {
		t.Errorf("code = %q, want %q", body.Code, CodeRateLimited)
	}
	if !strings.Contains(body.Message, "widgets") {
		t.Errorf("message %q does not say what was limited", body.Message)
	}
}

// TestHazardWriteRoutesAreRateLimited goes through the real route table. The
// requests are deliberately invalid (unknown kind), so they are rejected with a
// 400 before reaching the database; that still shows the limiter sits in front
// of the handler, because the sixth request is stopped before validation.
func TestHazardWriteRoutesAreRateLimited(t *testing.T) {
	s := authServer(t)
	h := s.Handler()
	bad := `{"location":{"lat":30.27,"lon":-97.74},"kind":"meteor"}`

	var statuses []int
	for i := 0; i < reportBurst+1; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authedRequest(t, s, "reporter", http.MethodPost, "/v1/hazards", bad))
		statuses = append(statuses, rec.Code)
	}
	for i, got := range statuses[:reportBurst] {
		if got != http.StatusBadRequest {
			t.Errorf("request %d status = %d, want 400 (allowed through, then invalid)", i+1, got)
		}
	}
	if got := statuses[reportBurst]; got != http.StatusTooManyRequests {
		t.Errorf("request %d status = %d, want 429", reportBurst+1, got)
	}

	// Confirm and resolve share a bucket that is separate from reports.
	for i := 0; i < voteBurst; i++ {
		path := "/v1/hazards/abc/confirm" // an invalid id: 400 before any database use
		if i%2 == 1 {
			path = "/v1/hazards/abc/resolve"
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authedRequest(t, s, "reporter", http.MethodPost, path, ""))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("vote %d status = %d, want 400 (the report bucket must not be shared)", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(t, s, "reporter", http.MethodPost, "/v1/hazards/abc/confirm", ""))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("vote %d status = %d, want 429", voteBurst+1, rec.Code)
	}

	// Another rider is unaffected.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(t, s, "someone-else", http.MethodPost, "/v1/hazards", bad))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a different user's first report status = %d, want 400", rec.Code)
	}
}

func TestHumanWait(t *testing.T) {
	cases := map[time.Duration]string{
		time.Second:                 "1 second",
		30 * time.Second:            "30 seconds",
		89 * time.Second:            "89 seconds",
		3 * time.Minute:             "3 minutes",
		2*time.Minute + time.Second: "3 minutes",
	}
	for d, want := range cases {
		if got := humanWait(d); got != want {
			t.Errorf("humanWait(%v) = %q, want %q", d, got, want)
		}
	}
}
