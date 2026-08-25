package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/cliclient"
	"github.com/levimackay/beacon/internal/protocol"
)

const testToken = "test-token-do-not-print-me"

// newTestClientFn returns a newClient func wired to srv, authenticating
// with testToken.
func newTestClientFn(srv *httptest.Server) func() (*cliclient.Client, error) {
	return func() (*cliclient.Client, error) {
		return &cliclient.Client{BaseURL: srv.URL, Token: testToken, HTTP: srv.Client()}, nil
	}
}

func sampleSnapshot(overall protocol.State) protocol.Snapshot {
	return protocol.Snapshot{
		GeneratedAt: time.Now(),
		Overall:     overall,
		Hub:         protocol.HubInfo{Version: "0.1.0"},
		Counts:      protocol.Counts{Healthy: 1},
		Targets: []protocol.TargetStatus{{
			Target: protocol.Target{ID: "web-1", Kind: protocol.KindWebsite, Name: "Portfolio", Address: "https://example.com", IntervalSeconds: 60, Enabled: true},
			State:  overall,
		}},
	}
}

func runCmd(t *testing.T, args []string, newClient func() (*cliclient.Client, error)) (stdout, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = run(context.Background(), args, &out, &errBuf, newClient)
	return out.String(), errBuf.String(), code
}

func TestStatusRendersSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sampleSnapshot(protocol.StateHealthy))
	}))
	defer srv.Close()

	stdout, stderr, code := runCmd(t, []string{"status"}, newTestClientFn(srv))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Portfolio") {
		t.Fatalf("stdout missing target name: %q", stdout)
	}
}

func TestStatusUnreachable(t *testing.T) {
	newClient := func() (*cliclient.Client, error) {
		return &cliclient.Client{BaseURL: "http://127.0.0.1:1", Token: testToken, HTTP: &http.Client{Timeout: 2 * time.Second}}, nil
	}
	stdout, stderr, code := runCmd(t, []string{"status"}, newClient)
	_ = stdout
	if code == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(stderr, "not responding") {
		t.Fatalf("stderr = %q, want it to mention 'not responding'", stderr)
	}
	if strings.Contains(stderr, "dial tcp") || strings.Contains(stderr, "connect:") {
		t.Fatalf("stderr leaked raw Go network error prose: %q", stderr)
	}
}

func TestStatusUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	stdout, stderr, code := runCmd(t, []string{"status"}, newTestClientFn(srv))
	if code == 0 {
		t.Fatalf("expected non-zero exit code, stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "token") {
		t.Fatalf("stderr = %q, want it to mention the token", stderr)
	}
	if strings.Contains(stderr, testToken) || strings.Contains(stdout, testToken) {
		t.Fatalf("token value leaked into output: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestStatusJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sampleSnapshot(protocol.StateHealthy))
	}))
	defer srv.Close()

	stdout, stderr, code := runCmd(t, []string{"status", "--json"}, newTestClientFn(srv))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	var v any
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
}

func TestStatusDownIsStillExitZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sampleSnapshot(protocol.StateDown))
	}))
	defer srv.Close()

	_, stderr, code := runCmd(t, []string{"status"}, newTestClientFn(srv))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 even though infrastructure is down; stderr = %q", code, stderr)
	}
}

func TestAddPostsExpectedBody(t *testing.T) {
	var got protocol.Target
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stdout, stderr, code := runCmd(t, []string{"add", "https://example.com", "--name", "Example", "--every", "45s"}, newTestClientFn(srv))
	if code != 0 {
		t.Fatalf("exit code = %d, stdout=%q stderr=%q", code, stdout, stderr)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/targets" {
		t.Fatalf("wrong request: %s %s", gotMethod, gotPath)
	}
	if !got.Enabled {
		t.Error("posted target should be Enabled")
	}
	if got.Kind != protocol.KindWebsite {
		t.Errorf("Kind = %q, want website", got.Kind)
	}
	if got.IntervalSeconds != 45 {
		t.Errorf("IntervalSeconds = %d, want 45", got.IntervalSeconds)
	}
	if got.AllowPrivate {
		t.Error("AllowPrivate should be false without --private")
	}
}

func TestAddPrivateFlag(t *testing.T) {
	var got protocol.Target
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	_, stderr, code := runCmd(t, []string{"add", "https://example.com", "--name", "Example", "--private"}, newTestClientFn(srv))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if !got.AllowPrivate {
		t.Error("AllowPrivate should be true with --private")
	}
}

func TestAddRejectsShortInterval(t *testing.T) {
	requested := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
	}))
	defer srv.Close()

	_, stderr, code := runCmd(t, []string{"add", "https://example.com", "--name", "Example", "--every", "1s"}, newTestClientFn(srv))
	if code == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if stderr == "" {
		t.Fatal("expected an explanatory message on stderr")
	}
	if requested {
		t.Fatal("no HTTP request should have been made")
	}
}

func TestAddRequiresName(t *testing.T) {
	requested := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
	}))
	defer srv.Close()

	_, stderr, code := runCmd(t, []string{"add", "https://example.com"}, newTestClientFn(srv))
	if code == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if !strings.Contains(stderr, "name") {
		t.Fatalf("stderr = %q, want it to mention --name", stderr)
	}
	if requested {
		t.Fatal("no HTTP request should have been made")
	}
}

func TestUnknownCommandExits2(t *testing.T) {
	_, stderr, code := runCmd(t, []string{"bogus"}, nil)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stderr == "" {
		t.Fatal("expected usage on stderr")
	}
}

func TestHelpExits0(t *testing.T) {
	stdout, _, code := runCmd(t, []string{"--help"}, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: beacon") {
		t.Fatalf("stdout missing usage: %q", stdout)
	}

	stdout, _, code = runCmd(t, []string{"help"}, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: beacon") {
		t.Fatalf("stdout missing usage: %q", stdout)
	}
}

func TestNoColorProducesNoANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sampleSnapshot(protocol.StateHealthy))
	}))
	defer srv.Close()

	stdout, stderr, code := runCmd(t, []string{"status"}, newTestClientFn(srv))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("stdout contains ANSI escapes with NO_COLOR set: %q", stdout)
	}
}
