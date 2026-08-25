package collect

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

const (
	webTimeout   = 10 * time.Second
	maxRedirects = 3
	// maxBodyReadBytes caps how much of a response body the collector will
	// ever look at, whether draining a body it doesn't otherwise care
	// about or checking one against Target.Contains. A monitor's job is to
	// notice a broken site quickly and repeatedly, not to buffer whatever
	// a misconfigured or hostile server decides to send back (a target is
	// an address the operator supplied, but the server behind it answers
	// with content the operator does not control). 256 KiB comfortably
	// holds a defaced page's or a parked-domain placeholder's visible
	// text, which sits near the top of the document, ahead of any large
	// embedded scripts or images, while keeping memory bounded even when
	// every website target is checked at once.
	maxBodyReadBytes = 256 * 1024
	certWarnWithin   = 14 * 24 * time.Hour
)

var errRedirectLimit = errors.New("redirect limit exceeded")

// guardedClient pairs an http.Client with the Guard that gates every
// connection it makes, so the two can never drift apart.
type guardedClient struct {
	guard  *Guard
	client *http.Client
}

func newGuardedClient(g *Guard) *guardedClient {
	return &guardedClient{
		guard: g,
		client: &http.Client{
			Timeout:   webTimeout,
			Transport: &http.Transport{DialContext: g.DialContext},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					return errRedirectLimit
				}
				// The redirected request carries the original
				// request's context, so a cancelled probe
				// cancels this lookup too.
				return g.CheckURL(req.Context(), req.URL.String())
			},
		},
	}
}

type webCollector struct {
	clock clock.Clock
	// public gates targets against every private range. private is used
	// only for targets the operator explicitly marked as living on their
	// own network. Two clients rather than one mutable Guard, because a
	// Guard shared across concurrently collected targets could otherwise
	// have its permissiveness flipped underneath an in-flight request.
	public  *guardedClient
	private *guardedClient
}

// NewWeb returns a Collector that probes HTTP(S) targets through g, which
// gates both the initial connection and every redirect hop against SSRF.
// Targets marked AllowPrivate are additionally permitted to reach the
// operator's own networks.
func NewWeb(c clock.Clock, g *Guard) Collector {
	return &webCollector{
		clock:   c,
		public:  newGuardedClient(g),
		private: newGuardedClient(&Guard{AllowPrivate: true}),
	}
}

// clientFor returns the guarded client appropriate to the target. A target
// may only widen what it reaches by opting in explicitly.
func (w *webCollector) clientFor(t protocol.Target) *guardedClient {
	if t.AllowPrivate {
		return w.private
	}
	return w.public
}

func (w *webCollector) Collect(ctx context.Context, t protocol.Target) protocol.Sample {
	s := protocol.Sample{
		TargetID: t.ID,
		At:       w.clock.Now(),
		Metrics:  make(map[string]float64),
	}

	gc := w.clientFor(t)

	if err := gc.guard.CheckURL(ctx, t.Address); err != nil {
		s.State = protocol.StateDown
		s.Error = err.Error()
		return s
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Address, nil)
	if err != nil {
		s.State = protocol.StateDown
		s.Error = err.Error()
		return s
	}

	start := time.Now()
	resp, err := gc.client.Do(req)
	s.LatencyMS = float64(time.Since(start)) / float64(time.Millisecond)
	if err != nil {
		s.State = protocol.StateDown
		s.Error = sanitizeErr(err)
		return s
	}
	defer resp.Body.Close()

	// The body is only actually buffered when a target asks for a content
	// check; every other target still has its response drained (so the
	// connection can be reused) without paying for an allocation it has
	// no use for.
	var bodyBuf bytes.Buffer
	dest := io.Writer(io.Discard)
	if t.Contains != "" {
		dest = &bodyBuf
	}
	_, _ = io.Copy(dest, io.LimitReader(resp.Body, maxBodyReadBytes))

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		expiry := resp.TLS.PeerCertificates[0].NotAfter
		s.CertExpiry = &expiry
		s.Metrics[protocol.MetricCertDaysLeft] = time.Until(expiry).Hours() / 24
	}

	expect := t.ExpectStatus
	if expect == 0 {
		expect = http.StatusOK
	}

	switch {
	case resp.StatusCode != expect:
		s.State = protocol.StateDown
		s.Error = fmt.Sprintf("status %d, expected %d", resp.StatusCode, expect)
	case t.Contains != "" && !bodyContains(bodyBuf.Bytes(), t.Contains):
		s.State = protocol.StateDown
		s.Error = fmt.Sprintf("response body does not contain %q", t.Contains)
	case s.CertExpiry != nil && time.Until(*s.CertExpiry) <= certWarnWithin:
		s.State = protocol.StateWarning
		s.Error = fmt.Sprintf("certificate expires %s", s.CertExpiry.Format(time.RFC3339))
	case warnAfterExceeded(s.LatencyMS, t.WarnAfterMS):
		s.State = protocol.StateWarning
		s.Error = fmt.Sprintf("response took %.0fms, warn threshold is %dms", s.LatencyMS, t.WarnAfterMS)
	default:
		s.State = protocol.StateHealthy
	}

	return s
}

// bodyContains reports whether body contains want, case-insensitively. Case
// is ignored because the assertion exists to catch missing or replaced
// content, a blank page, a defaced site, a parked-domain placeholder, not to
// police a site's capitalization; a check that broke every time a page's own
// markup changed "Welcome" to "welcome" would just teach the operator to
// ignore it.
func bodyContains(body []byte, want string) bool {
	return bytes.Contains(bytes.ToLower(body), bytes.ToLower([]byte(want)))
}

// warnAfterExceeded reports whether a response's latency has gone past a
// configured warn-after threshold. A zero threshold means none is
// configured, so it never trips. The comparison is strict: a response that
// lands exactly on the threshold has not yet gone after it.
func warnAfterExceeded(latencyMS float64, warnAfterMS int) bool {
	return warnAfterMS > 0 && latencyMS > float64(warnAfterMS)
}

// sanitizeErr reduces a request error to a message safe to store and fit to
// show a person: no full request URL (which may carry a query string in it),
// and a plain phrase rather than Go's wrapped-error prose.
//
// This is the line the user reads when something is broken, often the only
// one they read, so "connection refused" is worth the mapping over
// "Get \"https://x/y?token=..\": dial tcp 10.0.0.1:443: connect: connection
// refused".
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}

	var uerr *url.Error
	if errors.As(err, &uerr) {
		if uerr.Timeout() {
			return "connection timeout"
		}
		if errors.Is(uerr.Err, errRedirectLimit) {
			return errRedirectLimit.Error()
		}
		return sanitizeErr(uerr.Err)
	}

	// The guard's own refusals are already written for a person.
	if errors.Is(err, ErrUnresolvable) {
		return err.Error()
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return "host could not be resolved"
		}
		return "DNS lookup failed"
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "TLS certificate could not be verified"
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return "TLS certificate does not match the hostname"
	}
	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		return "TLS certificate is not from a trusted authority"
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection refused"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection reset"
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return "host unreachable"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "connection timeout"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "the server closed the connection"
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		// An OpError carries the address in its prose; keep only the
		// innermost cause.
		return sanitizeErr(opErr.Err)
	}

	return err.Error()
}
