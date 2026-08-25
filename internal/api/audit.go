package api

import (
	"context"
	"net/http"
	"time"
)

// auditTargetKey is the context key under which a mutating handler's target
// id is stashed, so the wrapping audit call can find it after the handler
// has run without the handler needing to know about auditing itself.
type auditTargetKey struct{}

// setAuditTarget records the id a mutating handler acted on (or attempted
// to act on), so the enclosing auditWrap can log it even on failure. A
// handler not reached through auditWrap is a silent no-op, which keeps this
// safe to call from shared code paths.
func setAuditTarget(r *http.Request, id string) {
	if p, ok := r.Context().Value(auditTargetKey{}).(*string); ok {
		*p = id
	}
}

// statusRecorder captures the status code a handler wrote, defaulting to
// 200 to match http.ResponseWriter's own "no WriteHeader call means OK"
// behaviour.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// auditWrap wraps a mutating handler so exactly one audit row is written per
// request, whether the handler succeeds, fails, or panics. It sits between
// auth and the handler in the middleware order.
//
// The audit write is deferred rather than merely following the handler
// call. A handler that panics after its store write has already committed
// would otherwise unwind straight past the write, be recovered by the outer
// recovery middleware, and return a 500: state changed, and no record of it
// exists. An audit trail that a crash can erase is not an audit trail, and
// a panic is exactly the circumstance in which one matters most.
//
// The panic is re-raised after the row is written, so the outer recovery
// middleware still logs it and still returns 500.
func (s *server) auditWrap(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := new(string)
		r = r.WithContext(context.WithValue(r.Context(), auditTargetKey{}, target))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		var panicked any
		func() {
			defer func() {
				if p := recover(); p != nil {
					panicked = p
				}
			}()
			next(rec, r)
		}()

		result := "ok"
		switch {
		case panicked != nil:
			result = "panic"
		case rec.status >= 400:
			result = "error"
		}

		// The request context may already be cancelled if the client
		// disconnected, and the audit row must be written regardless
		// of whether anyone is still listening for the response.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), auditWriteTimeout)
		defer cancel()
		if err := s.deps.Store.Audit(ctx, principalName, action, *target, result); err != nil {
			s.deps.Logger.Error("audit write failed",
				"action", action, "target", *target, "result", result, "err", err)
		}

		if panicked != nil {
			panic(panicked)
		}
	}
}

// auditWriteTimeout bounds the audit write so a stalled database cannot
// hold a request handler open indefinitely.
const auditWriteTimeout = 5 * time.Second
