package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/wesishee/viatuta/internal/auth"
	"github.com/wesishee/viatuta/internal/config"
	"github.com/wesishee/viatuta/internal/graph"
	"github.com/wesishee/viatuta/internal/hazard"
	"github.com/wesishee/viatuta/internal/store"
)

// Server holds everything the HTTP handlers need.
//
// Dependencies are fields rather than package globals so that a test can
// construct a Server with a test database and a discard logger. This is
// dependency injection without a framework — in Go it is just a struct.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	pool   *store.Pool

	// graph is the in-memory routing graph, loaded once at startup. It is
	// nil until loading finishes, which is what /readyz reports on.
	graph   *graph.Graph
	snapper *graph.Snapper

	// hazards is the live overlay of rider-reported hazards, kept outside
	// the graph because it changes between requests.
	hazards *hazard.Index

	// signer issues and verifies access tokens. It is nil when no signing
	// secret is configured, in which case the account endpoints report
	// themselves unavailable rather than falling back to something insecure.
	signer *auth.Signer

	// started is used by the health check to report uptime.
	started time.Time
}

// Hazards exposes the hazard overlay so startup can populate it.
func (s *Server) Hazards() *hazard.Index { return s.hazards }

// NewServer constructs a Server.
//
// g may be nil, in which case routing endpoints report themselves
// unavailable but health checks and future account endpoints still work.
func NewServer(cfg config.Config, logger *slog.Logger, pool *store.Pool, g *graph.Graph) *Server {
	s := &Server{
		cfg:     cfg,
		logger:  logger,
		pool:    pool,
		graph:   g,
		started: time.Now(),
	}
	if g != nil && pool != nil {
		s.snapper = graph.NewSnapper(pool, g)
		s.hazards = hazard.NewIndex(g.NumEdges())
	}

	// A missing or too-short secret disables accounts entirely rather than
	// signing tokens with something guessable. config.Load already refuses
	// to start in production without one.
	if signer, err := auth.NewSigner(cfg.JWTSecret); err == nil {
		s.signer = signer
	} else {
		logger.Warn("account endpoints disabled", "reason", err)
	}
	return s
}

// Handler returns the fully wired HTTP handler: route table plus middleware.
//
// Routes use Go 1.22+ pattern syntax — "METHOD /path/{param}" — so no
// third-party router is needed. Read a path parameter with r.PathValue("id").
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- Operational -------------------------------------------------
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	// --- Routing -----------------------------------------------------
	mux.HandleFunc("POST /v1/route/plan", s.handleRoutePlan)

	// --- Accounts ----------------------------------------------------
	mux.HandleFunc("POST /v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("GET /v1/auth/me", s.requireAuth(s.handleMe))

	// --- Saved routes ------------------------------------------------
	mux.HandleFunc("GET /v1/routes", s.requireAuth(s.handleListRoutes))
	mux.HandleFunc("POST /v1/routes", s.requireAuth(s.handleSaveRoute))
	mux.HandleFunc("GET /v1/routes/{id}", s.requireAuth(s.handleGetRoute))
	mux.HandleFunc("DELETE /v1/routes/{id}", s.requireAuth(s.handleDeleteRoute))

	// --- Hazard reports ----------------------------------------------
	// Anonymous reports are accepted, but an author is recorded when the
	// caller is signed in — which is what makes abuse traceable later.
	mux.HandleFunc("POST /v1/hazards", s.optionalAuth(s.handleReportHazard))
	mux.HandleFunc("GET /v1/hazards", s.handleListHazards)
	mux.HandleFunc("POST /v1/hazards/{id}/confirm", s.optionalAuth(s.handleConfirmHazard))

	// Outermost first: a panic inside logging should still be recovered,
	// and every log line should already have a request ID to report.
	return chain(mux,
		withRequestID,
		withRecover(s.logger),
		withLogging(s.logger),
		withCORS,
	)
}

// HealthResponse is returned by /healthz and /readyz.
type HealthResponse struct {
	Status   string `json:"status"`
	Uptime   string `json:"uptime"`
	Database string `json:"database,omitempty"`
	Graph    string `json:"graph,omitempty"`
	Nodes    int    `json:"nodes,omitempty"`
	Edges    int    `json:"edges,omitempty"`
}

// handleHealth reports that the process is alive.
//
// This deliberately does NOT check the database. A liveness probe answers
// "should this process be restarted?" — and restarting the API will not fix
// a down database, it will just add an outage.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status: "ok",
		Uptime: time.Since(s.started).Round(time.Second).String(),
	})
}

// handleReady reports whether the process can actually serve traffic.
//
// This one DOES check the database, because a readiness probe answers
// "should traffic be sent here?" and the answer is no if our dependencies
// are unreachable.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	resp := HealthResponse{
		Status: "ok",
		Uptime: time.Since(s.started).Round(time.Second).String(),
	}

	if err := s.pool.Ping(ctx); err != nil {
		s.logger.Warn("readiness check failed", "error", err)
		resp.Status = "degraded"
		resp.Database = "unreachable"
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp.Database = "ok"

	// Loading the graph takes seconds. Reporting ready before it finishes
	// would let a platform send traffic to an instance that cannot answer.
	if s.graph == nil {
		resp.Status = "starting"
		resp.Graph = "not loaded"
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp.Graph = "ok"
	resp.Nodes = s.graph.NumNodes()
	resp.Edges = s.graph.NumEdges()

	writeJSON(w, http.StatusOK, resp)
}
