package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerPrefix = "Bearer "

// extractBearer pulls the token out of an Authorization header. The second
// return is false for a missing header, a non-Bearer scheme, or a Bearer
// scheme with no value.
func extractBearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return "", false
	}
	tok := strings.TrimPrefix(h, bearerPrefix)
	if tok == "" {
		return "", false
	}
	return tok, true
}

// tokenEqual reports whether a and b are the same token, in constant time
// regardless of length. Hashing first, rather than comparing raw bytes,
// means a length mismatch never takes a different code path than a
// same-length mismatch, so neither the token's length nor its content can
// be inferred from timing.
func tokenEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}

// authMiddleware requires a valid bearer token on every request except
// GET /v1/health. A 401 always carries the same body, {"error":"unauthorized"},
// whatever was wrong with the request: missing header, malformed scheme,
// wrong token, or a same-length wrong token all look identical to the
// caller.
func (s *server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		tok, ok := extractBearer(r)
		if !ok || !tokenEqual(tok, s.deps.Token) {
			// Charge the failure against a dedicated, much tighter
			// budget so repeated guessing is throttled while a
			// correctly-authenticated client polling at its normal
			// rate is unaffected.
			if allowed, retryAfter := s.authLimiter.allow(clientKey(r)); !allowed {
				w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
				writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
