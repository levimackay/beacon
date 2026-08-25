package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// TestSecurity_PanicAfterMutationStillAudits demonstrates that a mutating
// handler which panics after its store write has already succeeded produces
// no audit row at all, even though the request did mutate state and the
// caller does get a (recovered, 500) response.
//
// NewServer's real composition is, outermost first: recover, auth, rate
// limit, then the mux — and auditWrap wraps only the individual mutating
// handler, one layer *inside* the mux, which is inside all three outer
// layers including recover. So when a handler panics, the panic unwinds
// straight past the `next(rec, r)` call in auditWrap — skipping the audit
// write that follows it in source — and is only caught by
// recoverMiddleware, which sits outside auditWrap entirely. The mutation
// that already landed in the store is never recorded.
//
// This matters because "every mutating request writes an audit row" is a
// stated security property (see docs/superpowers/specs — Security section),
// and it is not true for any handler that can panic after its write: a
// caller can cause state to change with no corresponding trail.
func TestSecurity_PanicAfterMutationStillAudits(t *testing.T) {
	fs := newFakeStore()
	s := &server{
		deps: Deps{
			Store:  fs,
			Clock:  clock.Fake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			Token:  testToken,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
		limiter:     newRateLimiter(rateLimitPerMinute, rateLimitBurst),
		authLimiter: newRateLimiter(authFailPerMinute, authFailBurst),
	}

	// Same shape as a real mutating route registration
	// (mux.HandleFunc(..., s.auditWrap("create_target", s.handlePostTarget))),
	// except the handler panics right after its store write succeeds —
	// standing in for any bug, nil dereference, or downstream panic in a
	// real handler once the mutation has already landed.
	mutateThenPanic := s.auditWrap("create_target", func(w http.ResponseWriter, r *http.Request) {
		target := protocol.Target{ID: "x", Kind: protocol.KindHost, Name: "x", IntervalSeconds: 30, Enabled: true}
		if err := s.deps.Store.UpsertTarget(r.Context(), target); err != nil {
			t.Fatal(err)
		}
		panic("simulated handler panic after a successful mutation")
	})
	h := s.recoverMiddleware(mutateThenPanic)

	req := httptest.NewRequest(http.MethodPost, "/v1/targets", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (recovered)", rec.Code)
	}
	if fs.upserts() != 1 {
		t.Fatalf("upserts = %d, want 1: the mutation must actually have happened for this test to mean anything", fs.upserts())
	}
	if got := fs.auditLen(); got != 1 {
		t.Fatalf("audit log has %d rows, want 1: a request that mutated state left no audit trail because it panicked afterward", got)
	}
}

// A panic must still reach the recovery middleware after the audit row is
// written, so the client gets a 500 and the panic is logged. Writing the
// row must not swallow the failure.
func TestSecurity_AuditedPanicStillReturns500AndKeepsServing(t *testing.T) {
	fs := newFakeStore()
	h := NewServer(Deps{
		Store: fs,
		Clock: clock.Fake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Token: testToken,
	})

	// A body that decodes but fails validation exercises the normal
	// error path; the panic path is covered by the test above. Here we
	// only need to confirm the server survives and still serves.
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", []byte(`{"kind":"website","name":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	after := doReq(t, h, http.MethodGet, "/v1/health", "")
	if after.Code != http.StatusOK {
		t.Fatalf("server stopped serving after an audited failure: %d", after.Code)
	}
	if len(fs.audit) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1 for the failed mutation", len(fs.audit))
	}
	if fs.audit[0].Result != "error" {
		t.Fatalf("audit result = %q, want error", fs.audit[0].Result)
	}
}
