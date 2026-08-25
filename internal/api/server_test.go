package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/store"
)

func authedReq(t *testing.T, h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestSnapshotETagAndUnknownTarget(t *testing.T) {
	h, fs := newTestServer(t)
	fs.targets["web-1"] = protocol.Target{
		ID: "web-1", Kind: protocol.KindHost, Name: "web 1",
		IntervalSeconds: 30, Enabled: true,
	}
	// deliberately no sample for web-1

	rec1 := authedReq(t, h, http.MethodGet, "/v1/snapshot", nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first snapshot status = %d, body=%s", rec1.Code, rec1.Body.String())
	}
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on snapshot response")
	}

	var snap protocol.Snapshot
	if err := json.Unmarshal(rec1.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snap.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(snap.Targets))
	}
	if snap.Targets[0].State != protocol.StateUnknown {
		t.Fatalf("state of sample-less target = %q, want unknown", snap.Targets[0].State)
	}
	if snap.Overall == protocol.StateHealthy {
		t.Fatalf("overall = healthy, want not-healthy for an unknown target")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/snapshot", nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("second snapshot status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Fatalf("304 body not empty: %q", rec2.Body.String())
	}
}

func TestPostTargetRejectsSSRFBeforeWrite(t *testing.T) {
	h, fs := newTestServer(t)
	body := []byte(`{"id":"evil","kind":"website","name":"metadata","address":"http://169.254.169.254/","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if fs.upserts() != 0 {
		t.Fatalf("upserts = %d, want 0: SSRF target must never be persisted", fs.upserts())
	}
}

func TestPostTargetNegativeInterval(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"host","name":"box","intervalSeconds":-5,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestPostTargetOversizedBody(t *testing.T) {
	h, _ := newTestServer(t)
	big := bytes.Repeat([]byte("a"), 2*1024*1024)
	payload := append([]byte(`{"id":"t1","kind":"host","name":"`), big...)
	payload = append(payload, []byte(`","intervalSeconds":30,"enabled":true}`)...)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", payload)
	if rec.Code < 400 {
		t.Fatalf("status = %d, want a 4xx rejection for an oversized body", rec.Code)
	}
}

func TestPostTargetMalformedJSONIs400(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", []byte(`{`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not valid JSON error shape: %v (%s)", err, rec.Body.String())
	}
	if body["error"] == "" {
		t.Fatal("error message empty")
	}
}

func TestDeleteUnknownTargetIs404(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodDelete, "/v1/targets/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAuditWritesOnePerMutationNoneOnGet(t *testing.T) {
	h, fs := newTestServer(t)

	// A GET writes nothing.
	authedReq(t, h, http.MethodGet, "/v1/targets", nil)
	if got := fs.auditLen(); got != 0 {
		t.Fatalf("audit rows after GET = %d, want 0", got)
	}

	// A successful mutation writes exactly one row.
	body := []byte(`{"id":"host-1","kind":"host","name":"host 1","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := fs.auditLen(); got != 1 {
		t.Fatalf("audit rows after successful POST = %d, want 1", got)
	}
	last := fs.audit[len(fs.audit)-1]
	if last.Result != "ok" || last.Target != "host-1" {
		t.Fatalf("unexpected audit row: %+v", last)
	}

	// A failed mutation still writes exactly one row, marked as a failure.
	badBody := []byte(`{"id":"host-2","kind":"host","name":"host 2","intervalSeconds":-1,"enabled":true}`)
	rec = authedReq(t, h, http.MethodPost, "/v1/targets", badBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want 400", rec.Code)
	}
	if got := fs.auditLen(); got != 2 {
		t.Fatalf("audit rows after failed POST = %d, want 2", got)
	}
	last = fs.audit[len(fs.audit)-1]
	if last.Result != "error" {
		t.Fatalf("failed mutation audit result = %q, want error", last.Result)
	}
}

func TestRateLimitReturns429WithRetryAfter(t *testing.T) {
	h, _ := newTestServer(t)

	var last *httptest.ResponseRecorder
	for i := 0; i < rateLimitBurst+1; i++ {
		last = authedReq(t, h, http.MethodGet, "/v1/diagnostics", nil)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status after burst+1 requests = %d, want 429", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatal("429 response missing Retry-After header")
	}
}

func TestPanicRecoveryKeepsServing(t *testing.T) {
	fs := newFakeStore()
	h := NewServer(Deps{
		Store: panicOnStatsStore{fs},
		Clock: clock.Fake(time.Now()),
		Token: testToken,
		Hub:   protocol.HubInfo{StartedAt: time.Now()},
	})

	rec := authedReq(t, h, http.MethodGet, "/v1/diagnostics", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status after panic = %d, want 500", rec.Code)
	}

	// The server must keep serving the next request.
	rec2 := authedReq(t, h, http.MethodGet, "/v1/targets", nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status after recovered panic = %d, want 200", rec2.Code)
	}
}

// panicOnStatsStore wraps a store.Store and panics on Stats, to exercise the
// recover middleware through a real dependency rather than reaching into
// the handler's internals.
type panicOnStatsStore struct{ *fakeStore }

func (panicOnStatsStore) Stats(context.Context) (store.Stats, error) {
	panic("boom")
}
