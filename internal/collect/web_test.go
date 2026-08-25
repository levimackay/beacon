package collect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
