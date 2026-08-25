package api

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Default rate limit: generous enough for normal polling, tight enough to
// bound a runaway client. One principal exists today, so this bounds one
// bucket; the design already keys on principal so it generalises once
// tailnet identity introduces more than one.
const (
	rateLimitPerMinute = 60
	rateLimitBurst     = 20
	bucketIdleTTL      = 10 * time.Minute

	// Failed authentication is limited far more tightly than ordinary
	// traffic, and separately from it, so that guessing the token is
	// bounded even while a legitimate client is polling normally.
	authFailPerMinute = 10
	authFailBurst     = 5
)

type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// rateLimiter is a token bucket per key.
type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	rate      float64 // tokens added per second
	burst     float64
	lastSweep time.Time
}

func newRateLimiter(perMinute, burst int) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
	}
}

// bucketFor returns the bucket for key, creating it if needed, and
// opportunistically evicts buckets idle longer than bucketIdleTTL so a
// stream of distinct principals (or forged tokens) doesn't grow the map
// forever.
//
// ponytail: the sweep is an O(n) scan over every bucket, done inline under
// the map lock. With one real principal today that's free. If tailnet
// identity brings many concurrent principals, move the sweep to a
// background ticker so it stops sharing the hot lock.
func (l *rateLimiter) bucketFor(key string) *tokenBucket {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: l.burst, lastSeen: time.Now()}
		l.buckets[key] = b
	}

	if time.Since(l.lastSweep) > bucketIdleTTL {
		l.lastSweep = time.Now()
		for k, other := range l.buckets {
			other.mu.Lock()
			idle := time.Since(other.lastSeen) > bucketIdleTTL
			other.mu.Unlock()
			if idle {
				delete(l.buckets, k)
			}
		}
	}
	return b
}

// allow reports whether one request for key may proceed, and if not, how
// long the caller should wait before retrying.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	b := l.bucketFor(key)
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.lastSeen = now
	b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// clientKey identifies an UNAUTHENTICATED caller, for throttling failed
// authentication only.
//
// It is deliberately NOT the bearer token. Keying on the credential being
// presented gives every guessed token its own fresh bucket, which makes the
// limiter useless against exactly the attack it exists to stop, and lets an
// unauthenticated caller grow the bucket map at will by varying the header.
// The remote address cannot be varied freely, so it is what the bucket
// hangs off.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func retryAfterSeconds(d time.Duration) string {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// rateLimitMiddleware bounds an authenticated client's request rate. It runs
// after authentication and keys on the principal, so a caller that never
// authenticates cannot consume the budget belonging to one that does.
func (s *server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := s.limiter.allow(principalName)
		if !allowed {
			w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
