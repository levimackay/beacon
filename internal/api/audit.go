package api

import (
	"context"
	"net/http"
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
// request, whether the handler succeeds or fails. It sits between auth and
// the handler in the middleware order.
func (s *server) auditWrap(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := new(string)
		r = r.WithContext(context.WithValue(r.Context(), auditTargetKey{}, target))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next(rec, r)

		result := "ok"
		if rec.status >= 400 {
			result = "error"
		}
		_ = s.deps.Store.Audit(r.Context(), principalName, action, *target, result)
	}
}
