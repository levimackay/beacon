package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

func testClient(h http.Handler) (*Client, func()) {
	srv := httptest.NewServer(h)
	return &Client{BaseURL: srv.URL, Token: "test-token", HTTP: srv.Client()}, srv.Close
}

func TestSnapshotSendsBearerTokenAndDecodes(t *testing.T) {
	var gotAuth string
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer done()

	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if snap.Overall != protocol.StateDown || len(snap.Targets) != 2 {
		t.Fatalf("decoded snapshot wrong: %+v", snap)
	}
	if c.LastLatency <= 0 {
		t.Error("LastLatency was not measured")
	}
}

func TestUnauthorizedIsAFriendlyError(t *testing.T) {
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer done()

	_, err := c.Snapshot(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), "test-token") {
		t.Fatal("error message leaked the token")
	}
}

func TestUnreachableHubIsNotAPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	c := &Client{BaseURL: url, Token: "t", HTTP: &http.Client{Timeout: time.Second}}
	_, err := c.Snapshot(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestServerErrorSurfacesTheHubsMessage(t *testing.T) {
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "interval must be at least 5 seconds"})
	}))
	defer done()

	err := c.AddTarget(context.Background(), protocol.Target{ID: "x"})
	if err == nil || !strings.Contains(err.Error(), "interval must be at least 5 seconds") {
		t.Fatalf("err = %v, want the hub's own message", err)
	}
}

func TestErrorBodyIsSizeLimited(t *testing.T) {
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(strings.Repeat("A", 1<<20)))
	}))
	defer done()

	err := c.AddTarget(context.Background(), protocol.Target{ID: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 4096 {
		t.Fatalf("error message was not capped: %d bytes", len(err.Error()))
	}
}

func TestDeleteTargetEscapesTheID(t *testing.T) {
	var gotPath string
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
	}))
	defer done()

	if err := c.DeleteTarget(context.Background(), "../../etc/passwd"); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
	if strings.Contains(gotPath, "..") && !strings.Contains(gotPath, "%2F") {
		t.Fatalf("path traversal was not escaped: %q", gotPath)
	}
}

func TestIncidentsBuildsQuery(t *testing.T) {
	var gotQuery string
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode([]protocol.Incident{})
	}))
	defer done()

	since := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	if _, err := c.Incidents(context.Background(), since, 50); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "since=2026-08-23T00%3A00%3A00Z") || !strings.Contains(gotQuery, "limit=50") {
		t.Fatalf("query = %q", gotQuery)
	}
}

func TestDiagnosticsStampsClientLatency(t *testing.T) {
	c, done := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(protocol.Diagnostics{SchedulerHealthy: true})
	}))
	defer done()

	d, err := c.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !d.SchedulerHealthy {
		t.Error("decode dropped a field")
	}
	if d.APILatencyMS <= 0 {
		t.Error("APILatencyMS was not stamped by the client")
	}
}
