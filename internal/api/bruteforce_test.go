package api

import (
	"net/http"
	"strconv"
	"testing"
)

// The pre-authentication rate limiter must not be keyed on the bearer
// token. If it were, every guessed token would arrive with its own fresh
// bucket and full burst allowance, so the limiter would place no bound at
// all on the one attack it exists to stop, and an unauthenticated caller
// could grow the bucket map indefinitely by varying the header.
func TestGuessingDistinctTokensIsThrottled(t *testing.T) {
	h, _ := newTestServer(t)

	throttledAt := -1
	for i := range 60 {
		// A different credential every time, which is exactly what a
		// brute-force attempt looks like.
		rec := doReq(t, h, http.MethodGet, "/v1/snapshot", "Bearer guess-"+strconv.Itoa(i))
		if rec.Code == http.StatusTooManyRequests {
			throttledAt = i
			break
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 or 429", i, rec.Code)
		}
	}

	if throttledAt < 0 {
		t.Fatal("60 distinct guessed tokens were never throttled: the limiter is keyed on attacker-controlled input")
	}
	if throttledAt > authFailBurst+2 {
		t.Fatalf("throttling only began at attempt %d, want it within a few of the %d-failure burst", throttledAt, authFailBurst)
	}
}

// A throttled caller must be told how long to wait, rather than being left
// to guess.
func TestAuthThrottleSetsRetryAfter(t *testing.T) {
	h, _ := newTestServer(t)

	for i := range 60 {
		rec := doReq(t, h, http.MethodGet, "/v1/snapshot", "Bearer guess-"+strconv.Itoa(i))
		if rec.Code != http.StatusTooManyRequests {
			continue
		}
		ra := rec.Header().Get("Retry-After")
		if ra == "" {
			t.Fatal("429 carried no Retry-After header")
		}
		secs, err := strconv.Atoi(ra)
		if err != nil || secs < 1 {
			t.Fatalf("Retry-After = %q, want a positive number of seconds", ra)
		}
		return
	}
	t.Fatal("never throttled")
}

// Throttling failed authentication must not lock out a client that is
// authenticating correctly. The two budgets are separate for this reason.
func TestFailedAuthDoesNotStarveTheValidClient(t *testing.T) {
	h, _ := newTestServer(t)

	// Exhaust the failure budget.
	for i := range 20 {
		doReq(t, h, http.MethodGet, "/v1/snapshot", "Bearer guess-"+strconv.Itoa(i))
	}

	rec := doReq(t, h, http.MethodGet, "/v1/snapshot", "Bearer "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token after a burst of failures = %d, want 200", rec.Code)
	}
}

// Health stays reachable while authentication is being throttled, so a
// diagnostic tool can still tell that the hub is alive.
func TestHealthSurvivesAuthThrottling(t *testing.T) {
	h, _ := newTestServer(t)

	for i := range 20 {
		doReq(t, h, http.MethodGet, "/v1/snapshot", "Bearer guess-"+strconv.Itoa(i))
	}

	rec := doReq(t, h, http.MethodGet, "/v1/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("health = %d during auth throttling, want 200", rec.Code)
	}
}
