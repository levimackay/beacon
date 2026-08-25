package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

const testToken = "s3cret-token-value"

func newTestServer(t *testing.T) (http.Handler, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	h := NewServer(Deps{
		Store: fs,
		Clock: clock.Fake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Token: testToken,
		Hub: protocol.HubInfo{
			Version:   "test",
			Host:      "test-host",
			StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	return h, fs
}

func doReq(t *testing.T, h http.Handler, method, path, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuth401Matrix(t *testing.T) {
	// A same-length wrong token, to exercise the constant-time compare
	// path rather than an early length-mismatch short circuit.
	sameLength := strings.Repeat("x", len(testToken))

	cases := []struct {
		name string
		auth string
	}{
		{"no header", ""},
		{"bearer no value", "Bearer"},
		{"bearer empty value", "Bearer "},
		{"basic scheme", "Basic " + testToken},
		{"wrong token", "Bearer wrong-token"},
		{"same length wrong token", "Bearer " + sameLength},
		{"prefix of real token", "Bearer " + testToken[:len(testToken)-1]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh server per case: failed attempts share a
			// deliberately small budget, so reusing one server
			// across the matrix would trip the auth throttle
			// instead of exercising the 401 path.
			h, _ := newTestServer(t)
			rec := doReq(t, h, http.MethodGet, "/v1/diagnostics", tc.auth)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			body := strings.TrimSpace(rec.Body.String())
			if body != `{"error":"unauthorized"}` {
				t.Fatalf("body = %q, want exactly {\"error\":\"unauthorized\"}", body)
			}
		})
	}
}

func TestAuthCorrectTokenSucceeds(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doReq(t, h, http.MethodGet, "/v1/diagnostics", "Bearer "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthUnauthenticated(t *testing.T) {
	h, _ := newTestServer(t)
	rec := doReq(t, h, http.MethodGet, "/v1/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"test-host", "targets", "hostname", "counts"} {
		if strings.Contains(body, leak) {
			t.Fatalf("health body leaked %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("health body missing status:ok: %s", body)
	}
}

// TestRouteAuthMatrix confirms no route is reachable unauthenticated except
// GET /v1/health.
func TestRouteAuthMatrix(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/health"},
		{http.MethodGet, "/v1/diagnostics"},
		{http.MethodGet, "/v1/snapshot"},
		{http.MethodGet, "/v1/targets"},
		{http.MethodPost, "/v1/targets"},
		{http.MethodDelete, "/v1/targets/some-id"},
		{http.MethodGet, "/v1/incidents"},
	}

	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			h, _ := newTestServer(t)
			rec := doReq(t, h, r.method, r.path, "")
			if r.path == "/v1/health" && r.method == http.MethodGet {
				if rec.Code == http.StatusUnauthorized {
					t.Fatalf("health should not require auth, got 401")
				}
				return
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without auth = %d, want 401", r.method, r.path, rec.Code)
			}
		})
	}
}
