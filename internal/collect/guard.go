// Package collect implements Beacon's collectors: the host collector reads
// local system metrics, the web collector probes HTTP(S) targets. Both are
// exposed through the Collector interface. Guard is the SSRF defense shared
// by anything in this package that dials a user-supplied address.
package collect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
)

// uniqueLocal is the IPv6 fc00::/7 unique-local range (RFC 4193). netip has
// no IsPrivate-equivalent helper for it, so it is checked explicitly.
var uniqueLocal = netip.MustParsePrefix("fc00::/7")

// carrierGrade is 100.64.0.0/10 (RFC 6598). netip.Addr.IsPrivate does not
// cover it, so without this an address in that range would be reachable
// while a plain LAN address was refused. It is also the range Tailscale
// assigns, which is exactly why it must be an explicit opt-in rather than an
// accidental hole: monitoring your own tailnet is legitimate, reaching an
// arbitrary CGNAT address from a cloud host is not.
var carrierGrade = netip.MustParsePrefix("100.64.0.0/10")

// metadataAddr is the cloud metadata endpoint (AWS/GCP/Azure all use it).
// It is technically link-local (169.254/16) and would already be rejected
// by IsLinkLocalUnicast, but it is named explicitly so the reason a request
// to it fails is unambiguous.
var metadataAddr = netip.MustParseAddr("169.254.169.254")

// ErrUnresolvable reports that a host could not be resolved at all, as
// distinct from resolving to an address Beacon refuses to reach.
//
// The difference matters at the point a target is added. A monitoring tool
// that refuses to accept a site because that site is currently down is
// backwards: being down is the condition the user wants to be told about.
// An unresolvable host is therefore accepted and reported as down, while a
// host resolving into a forbidden range is refused outright. The dial-time
// check still applies on every subsequent probe, so accepting the target
// grants it nothing.
var ErrUnresolvable = errors.New("host could not be resolved")

// Guard rejects requests aimed at loopback, private, carrier-grade-NAT,
// link-local, unique-local, unspecified, multicast or cloud-metadata
// addresses, closing the SSRF hole a monitoring tool would otherwise hand an
// attacker: "add a website target pointed at 169.254.169.254" turned into a
// credential leak.
type Guard struct {
	// AllowPrivate permits addresses on the operator's own networks:
	// loopback, RFC 1918, carrier-grade NAT (which is where Tailscale
	// lives), link-local and unique-local. It is set per target, by a
	// user who has explicitly said "this one is on my private network",
	// and by tests dialing httptest servers.
	//
	// It never permits the cloud metadata address. No legitimate reason
	// to monitor it exists, and that is the single endpoint whose
	// exposure turns an SSRF into stolen credentials.
	AllowPrivate bool
}

// NewGuard returns a Guard with default (non-permissive) settings.
func NewGuard() *Guard { return &Guard{} }

// blocked reports why addr is disallowed, or "" if it is fine.
func (g *Guard) blocked(addr netip.Addr) string {
	addr = addr.Unmap()

	// Checked before the AllowPrivate escape hatch, deliberately: the
	// metadata address is never reachable through Beacon, whatever the
	// target is configured to allow.
	if addr == metadataAddr {
		return "cloud metadata address"
	}
	if g.AllowPrivate {
		return ""
	}
	switch {
	case addr.IsLoopback():
		return "loopback address"
	case addr.IsPrivate():
		return "private address"
	case addr.Is4() && carrierGrade.Contains(addr):
		return "carrier-grade NAT address"
	case addr.IsLinkLocalUnicast():
		return "link-local address"
	case addr.Is6() && uniqueLocal.Contains(addr):
		return "unique-local address"
	case addr.IsUnspecified():
		return "unspecified address"
	case addr.IsMulticast():
		return "multicast address"
	}
	return ""
}

// CheckURL validates a target URL before any network activity: scheme must
// be http or https, and every address the host resolves to must pass the
// range check. This is a pre-flight check only — DialContext repeats the
// address check at dial time, because DNS can change between CheckURL and
// the actual connection (DNS rebinding).
func (g *Guard) CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q is not allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url has no host")
	}
	addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnresolvable, host)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: %s", ErrUnresolvable, host)
	}
	for _, a := range addrs {
		if reason := g.blocked(a); reason != "" {
			return fmt.Errorf("address %s is a %s", a, reason)
		}
	}
	return nil
}

// DialContext is a net.Dialer.DialContext-compatible function that re-checks
// the address actually being dialed, not just the address CheckURL saw. Wire
// it into an http.Transport.DialContext so every connection the web
// collector makes, including redirect hops, is range-checked at dial time.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		// http.Transport normally hands DialContext a hostname, not an IP
		// literal — resolution happens here, at dial time, which is what
		// closes the DNS-rebinding window: the IP checked below is the
		// exact IP dialed immediately after, not a name checked earlier.
		ips, lookupErr := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if lookupErr != nil || len(ips) == 0 {
			return nil, fmt.Errorf("resolve %s: %w", host, lookupErr)
		}
		ip = ips[0]
	}
	if reason := g.blocked(ip); reason != "" {
		return nil, fmt.Errorf("address %s is a %s", ip, reason)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}
