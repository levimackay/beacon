package collect

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

func target(addr string) protocol.Target {
	return protocol.Target{ID: "t1", Kind: protocol.KindWebsite, Name: "t1", Address: addr, IntervalSeconds: 30, Enabled: true}
}

func TestWeb_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), target(srv.URL))

	if s.State != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy (error: %s)", s.State, s.Error)
	}
	if s.LatencyMS <= 0 {
		t.Fatalf("latencyMs = %v, want > 0", s.LatencyMS)
	}
	if s.TargetID != "t1" {
		t.Fatalf("targetId = %q", s.TargetID)
	}
	if s.At.IsZero() {
		t.Fatal("At is zero")
	}
}

func TestWeb_StatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tgt := target(srv.URL)
	tgt.ExpectStatus = http.StatusOK
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateDown {
		t.Fatalf("state = %v, want down", s.State)
	}
	if s.Error == "" {
		t.Fatal("want non-empty Error")
	}
}

func TestWeb_ConnectionClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer srv.Close()

	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), target(srv.URL))

	if s.State != protocol.StateDown {
		t.Fatalf("state = %v, want down", s.State)
	}
	if s.Error == "" {
		t.Fatal("want non-empty Error")
	}
}

func TestWeb_RedirectLimit(t *testing.T) {
	mux := http.NewServeMux()
	for i := 0; i < 4; i++ {
		i := i
		mux.HandleFunc(fmt.Sprintf("/%d", i), func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, fmt.Sprintf("/%d", i+1), http.StatusFound)
		})
	}
	mux.HandleFunc("/4", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), target(srv.URL+"/0"))

	if s.State != protocol.StateDown {
		t.Fatalf("state = %v, want down", s.State)
	}
	if !strings.Contains(s.Error, "redirect limit") {
		t.Fatalf("error = %q, want it to mention the redirect limit", s.Error)
	}
}

func TestWeb_ErrorDoesNotLeakQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tgt := target(srv.URL + "/?token=super-secret-value")
	tgt.ExpectStatus = http.StatusOK
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if strings.Contains(s.Error, "super-secret-value") {
		t.Fatalf("error leaked the query string: %q", s.Error)
	}
}

func TestWeb_ContainsPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>Welcome to the site</body></html>")
	}))
	defer srv.Close()

	tgt := target(srv.URL)
	tgt.Contains = "Welcome"
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy (error: %s)", s.State, s.Error)
	}
}

func TestWeb_ContainsFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A blank 200 response is exactly the false-negative case a content
	// assertion exists to catch: something answered, but there is nothing
	// there.
	tgt := target(srv.URL)
	tgt.Contains = "Welcome"
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateDown {
		t.Fatalf("state = %v, want down", s.State)
	}
	if !strings.Contains(s.Error, "does not contain") {
		t.Fatalf("error = %q, want it to mention the missing text", s.Error)
	}
}

func TestWeb_ContainsIsCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>WELCOME</body></html>")
	}))
	defer srv.Close()

	tgt := target(srv.URL)
	tgt.Contains = "welcome"
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy for a case-only difference (error: %s)", s.State, s.Error)
	}
}

func TestWeb_ContainsRespectsTheBodyCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), maxBodyReadBytes))
		w.Write([]byte("Welcome"))
	}))
	defer srv.Close()

	// The expected text sits just past the cap, so it must not be found:
	// the cap exists precisely so the collector never reads an unbounded
	// amount of a response looking for a match.
	tgt := target(srv.URL)
	tgt.Contains = "Welcome"
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateDown {
		t.Fatalf("state = %v, want down: the match text is beyond the read cap", s.State)
	}
}

func TestWeb_WarnAfterExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tgt := target(srv.URL)
	tgt.WarnAfterMS = 5
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateWarning {
		t.Fatalf("state = %v, want warning (latency %.0fms, error: %s)", s.State, s.LatencyMS, s.Error)
	}
}

func TestWeb_WarnAfterNotConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A slow response with no threshold set must still be healthy: this
	// field is opt-in, and every target that predates it must keep
	// behaving exactly as it did before.
	tgt := target(srv.URL)
	c := NewWeb(clock.Real(), &Guard{AllowPrivate: true})
	s := c.Collect(context.Background(), tgt)

	if s.State != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy", s.State)
	}
}

// TestWarnAfterExceededBoundary exercises the exact comparison used to
// decide warning state, since driving a real HTTP round trip to land on a
// precise millisecond is not reliable.
func TestWarnAfterExceededBoundary(t *testing.T) {
	cases := []struct {
		name        string
		latencyMS   float64
		warnAfterMS int
		want        bool
	}{
		{"no threshold configured", 100000, 0, false},
		{"below threshold", 1999, 2000, false},
		{"exactly at threshold", 2000, 2000, false},
		{"just past threshold", 2000.001, 2000, true},
		{"well past threshold", 5000, 2000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := warnAfterExceeded(tc.latencyMS, tc.warnAfterMS); got != tc.want {
				t.Errorf("warnAfterExceeded(%v, %v) = %v, want %v", tc.latencyMS, tc.warnAfterMS, got, tc.want)
			}
		})
	}
}

func TestWeb_CertExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	guard := &Guard{AllowPrivate: true}
	// Built directly (package-internal) rather than through NewWeb so the
	// test can trust the httptest server's self-signed cert without
	// weakening the collector's normal TLS verification in production.
	gc := &guardedClient{
		guard: guard,
		client: &http.Client{
			Transport: &http.Transport{
				DialContext:     guard.DialContext,
				TLSClientConfig: &tls.Config{RootCAs: pool},
			},
		},
	}
	wc := &webCollector{clock: clock.Real(), public: gc, private: gc}

	s := wc.Collect(context.Background(), target(srv.URL))

	if s.State != protocol.StateHealthy && s.State != protocol.StateWarning {
		t.Fatalf("state = %v, want healthy or warning (error: %s)", s.State, s.Error)
	}
	if s.CertExpiry == nil {
		t.Fatal("want CertExpiry populated for an https target")
	}
	if _, ok := s.Metrics[protocol.MetricCertDaysLeft]; !ok {
		t.Fatal("want cert_days_left metric set")
	}
}
