package collect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// A target added without the opt-in must not be able to reach the machine
// Beacon itself runs on. This is the difference between a monitoring tool
// and a port scanner someone else drives.
func TestWebRefusesLoopbackWithoutOptIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewWeb(clock.Real(), NewGuard())
	s := c.Collect(context.Background(), protocol.Target{
		ID: "web-1", Kind: protocol.KindWebsite, Name: "Local", Address: srv.URL, IntervalSeconds: 60,
	})

	if s.State != protocol.StateDown {
		t.Fatalf("state = %q, want down: a public target must not reach loopback", s.State)
	}
	if !strings.Contains(s.Error, "loopback") {
		t.Fatalf("error = %q, want it to name the loopback refusal", s.Error)
	}
}

// With the opt-in, the same target works. This is what makes monitoring a
// service on the operator's own LAN or tailnet possible.
func TestWebReachesLoopbackWithOptIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewWeb(clock.Real(), NewGuard())
	s := c.Collect(context.Background(), protocol.Target{
		ID: "web-1", Kind: protocol.KindWebsite, Name: "Local", Address: srv.URL,
		IntervalSeconds: 60, AllowPrivate: true,
	})

	if s.State != protocol.StateHealthy {
		t.Fatalf("state = %q (error %q), want healthy with the opt-in set", s.State, s.Error)
	}
}

// The opt-in widens reach to private networks only. The metadata endpoint
// stays refused, because no monitoring use for it exists and its exposure
// is how an SSRF becomes stolen credentials.
func TestOptInStillRefusesCloudMetadata(t *testing.T) {
	c := NewWeb(clock.Real(), NewGuard())
	s := c.Collect(context.Background(), protocol.Target{
		ID: "web-1", Kind: protocol.KindWebsite, Name: "Metadata",
		Address: "http://169.254.169.254/latest/meta-data/", IntervalSeconds: 60,
		AllowPrivate: true,
	})

	if s.State != protocol.StateDown {
		t.Fatalf("state = %q, want down: metadata must never be reachable", s.State)
	}
	if !strings.Contains(s.Error, "metadata") {
		t.Fatalf("error = %q, want it to name the metadata refusal", s.Error)
	}
}

// One target opting in must not widen what a different target may reach.
func TestOptInDoesNotLeakBetweenTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewWeb(clock.Real(), NewGuard())
	privileged := protocol.Target{ID: "a", Kind: protocol.KindWebsite, Name: "Local", Address: srv.URL, IntervalSeconds: 60, AllowPrivate: true}
	ordinary := protocol.Target{ID: "b", Kind: protocol.KindWebsite, Name: "Public", Address: srv.URL, IntervalSeconds: 60}

	if s := c.Collect(context.Background(), privileged); s.State != protocol.StateHealthy {
		t.Fatalf("privileged target state = %q (%s)", s.State, s.Error)
	}
	if s := c.Collect(context.Background(), ordinary); s.State != protocol.StateDown {
		t.Fatalf("ordinary target state = %q, want down; permissiveness leaked between targets", s.State)
	}
}
