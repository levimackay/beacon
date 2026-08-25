// Package api implements the hub's local HTTP API: a bearer-authenticated,
// loopback-only surface that the CLI, the Mac app and the widget all poll.
package api

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/store"
)

// schedulerHealthyWindow is how stale the scheduler's last tick may be
// before diagnostics reports it unhealthy.
const schedulerHealthyWindow = 2 * time.Minute

// principalName identifies the single credential Beacon has today. It is a
// fixed string rather than derived from the token so that audit rows and
// rate-limit buckets read sensibly once tailnet identity replaces the bearer
// token as the source of "who is this".
const principalName = "local"

// SchedulerInfo is the small slice of the scheduler that diagnostics needs.
type SchedulerInfo interface {
	LastTick() time.Time
}

// Deps are everything the API needs to serve a request. Scheduler and
// Logger may be nil: a nil Scheduler means "not running" (diagnostics
// reports it unhealthy), and a nil Logger falls back to a discard logger.
type Deps struct {
	Store     store.Store
	Clock     clock.Clock
	Token     string
	Hub       protocol.HubInfo
	Guard     *collect.Guard
	Scheduler SchedulerInfo
	Logger    *slog.Logger
}

type server struct {
	deps        Deps
	limiter     *rateLimiter
	authLimiter *rateLimiter
}

// NewServer builds the hub's HTTP API. Middleware order, outermost first:
// recover, auth, rate limit, audit (per mutating route), handler.
//
// The general rate limiter sits INSIDE authentication, keyed on the
// authenticated principal, and a separate, much tighter limiter throttles
// failed authentication, keyed on the caller's address. Putting the general
// limiter outside auth was tried and is wrong: the hub listens on loopback,
// so every caller shares one address and one bucket, and a local process
// spraying bad tokens would exhaust it and lock the user's own Mac app out
// of its data. Splitting the budgets means guessing is bounded without a
// failed attacker being able to starve a correctly authenticated client.
func NewServer(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Guard == nil {
		d.Guard = collect.NewGuard()
	}
	s := &server{
		deps:        d,
		limiter:     newRateLimiter(rateLimitPerMinute, rateLimitBurst),
		authLimiter: newRateLimiter(authFailPerMinute, authFailBurst),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("GET /v1/snapshot", s.handleSnapshot)
	mux.HandleFunc("GET /v1/targets", s.handleListTargets)
	mux.HandleFunc("POST /v1/targets", s.auditWrap("create_target", s.handlePostTarget))
	mux.HandleFunc("DELETE /v1/targets/{id}", s.auditWrap("delete_target", s.handleDeleteTarget))
	mux.HandleFunc("GET /v1/incidents", s.handleListIncidents)

	var h http.Handler = mux
	h = s.rateLimitMiddleware(h)
	h = s.authMiddleware(h)
	h = s.recoverMiddleware(h)
	return h
}

// hubInfo returns Deps.Hub with UptimeSeconds recomputed from the clock, so
// it reflects "now" rather than whatever was true at process start.
func (s *server) hubInfo() protocol.HubInfo {
	h := s.deps.Hub
	h.UptimeSeconds = int64(s.deps.Clock.Now().Sub(h.StartedAt).Seconds())
	return h
}

func (s *server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.deps.Logger.Error("panic recovered",
					"error", rec, "method", r.Method, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
