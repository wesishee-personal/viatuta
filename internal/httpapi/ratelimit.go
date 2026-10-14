package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Per-user limits on hazard writes.
//
// They are keyed on the signed-in user, not the IP address: phones on one
// cellular network share addresses, and riders at the same café would
// otherwise throttle each other. Hazard writes require an account, so there is
// always a user to key on.
//
// The two buckets are separate so a burst of confirmations does not use up a
// rider's report allowance. Each allows a short burst (a rider who spots five
// hazards on one block can report them) and then refills steadily.
const (
	reportsPerHour = 20
	reportBurst    = 5

	// Confirming and resolving share one bucket: both are a rider answering
	// "is this still there?" about hazards that already exist.
	votesPerHour = 60
	voteBurst    = 10

	// limiterIdleTTL is how long an unused limiter is kept. A full bucket and
	// a fresh one behave identically, so forgetting an idle user is lossless.
	limiterIdleTTL = time.Hour
)

// userLimiters hands out a token bucket per user.
//
// It lives in memory, so it resets when the process restarts and is per
// instance. That is fine for a single API container. Run several and a rider
// could get the limit multiplied by the instance count; at that point the
// counts belong in Postgres or Redis.
type userLimiters struct {
	limit rate.Limit
	burst int
	now   func() time.Time // replaceable in tests

	mu        sync.Mutex
	byUser    map[string]*userLimiter
	lastSweep time.Time
}

type userLimiter struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

func newUserLimiters(perHour, burst int) *userLimiters {
	return &userLimiters{
		limit:  rate.Every(time.Hour / time.Duration(perHour)),
		burst:  burst,
		now:    time.Now,
		byUser: make(map[string]*userLimiter),
	}
}

// allow takes a token for the user. When none is available it returns false
// and how long until one will be.
func (u *userLimiters) allow(userID string) (bool, time.Duration) {
	now := u.now()

	u.mu.Lock()
	defer u.mu.Unlock()

	u.sweepLocked(now)

	ul, ok := u.byUser[userID]
	if !ok {
		ul = &userLimiter{lim: rate.NewLimiter(u.limit, u.burst)}
		u.byUser[userID] = ul
	}
	ul.lastSeen = now

	// Reserve, and hand the token back if it would have to wait: a request
	// that is refused must not also spend the allowance.
	r := ul.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, time.Hour
	}
	if wait := r.DelayFrom(now); wait > 0 {
		r.CancelAt(now)
		return false, wait
	}
	return true, 0
}

// sweepLocked drops limiters idle longer than limiterIdleTTL. It runs at most
// every half-TTL, so the cost is amortised across requests rather than needing
// a background goroutine.
func (u *userLimiters) sweepLocked(now time.Time) {
	if now.Sub(u.lastSweep) < limiterIdleTTL/2 {
		return
	}
	u.lastSweep = now
	for id, ul := range u.byUser {
		if now.Sub(ul.lastSeen) > limiterIdleTTL {
			delete(u.byUser, id)
		}
	}
}

// rateLimited wraps a handler that sits behind requireAuth, so UserIDFrom is
// always set. Requests over the limit get 429 with a Retry-After header.
//
// The limit is applied before the handler validates the request, so malformed
// requests spend allowance too. That is deliberate: they are exactly what a
// misbehaving client sends in a loop.
func (s *Server) rateLimited(l *userLimiters, what string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, wait := l.allow(UserIDFrom(r.Context()))
		if !ok {
			secs := int(math.Ceil(wait.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeError(w, http.StatusTooManyRequests, CodeRateLimited,
				"too many "+what+"; try again in "+humanWait(wait))
			return
		}
		next(w, r)
	}
}

// humanWait renders a wait for a message: "45 seconds", "3 minutes".
func humanWait(d time.Duration) string {
	if d < 90*time.Second {
		s := int(math.Ceil(d.Seconds()))
		if s == 1 {
			return "1 second"
		}
		return strconv.Itoa(s) + " seconds"
	}
	m := int(math.Ceil(d.Minutes()))
	return strconv.Itoa(m) + " minutes"
}
