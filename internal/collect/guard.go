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
	"strings"
	"time"
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

// limitedBroadcast is 255.255.255.255 (RFC 919), the IPv4 limited-broadcast
// address. netip.Addr has no IsBroadcast-equivalent helper, so it is
// checked explicitly, same as carrierGrade and uniqueLocal above.
var limitedBroadcast = netip.MustParseAddr("255.255.255.255")

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

// Well-known IPv6 prefixes that carry an IPv4 address inside them. An
// address in any of these reaches the embedded IPv4 destination on a
// network that implements the corresponding translation, so the embedded
// address is what the range check has to be applied to.
//
// netip.Addr.Unmap only folds the IPv4-mapped form (::ffff:a.b.c.d).
// Checking only that leaves every other form as a way around the guard:
// 64:ff9b::a9fe:a9fe reaches the cloud metadata endpoint on any DNS64/NAT64
// network, which is the exact destination the guard exists to refuse.
var (
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")   // RFC 6052
	nat64LocalUse  = netip.MustParsePrefix("64:ff9b:1::/48") // RFC 8215
	sixToFour      = netip.MustParsePrefix("2002::/16")      // RFC 3056
	teredo         = netip.MustParsePrefix("2001::/32")      // RFC 4380
)

// embeddedIPv4 extracts the IPv4 address carried inside an IPv6 address,
// for the translation forms that route to it. The second result reports
// whether one was found.
func embeddedIPv4(a netip.Addr) (netip.Addr, bool) {
	if !a.Is6() {
		return netip.Addr{}, false
	}
	b := a.As16()

	switch {
	case nat64WellKnown.Contains(a), nat64LocalUse.Contains(a):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true

	case sixToFour.Contains(a):
		// 2002:V4ADDR::/48 carries the address in bytes 2 through 5.
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true

	case teredo.Contains(a):
		// The Teredo client address in the last four bytes is stored
		// obfuscated, as the bitwise complement of the real address.
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	}

	// IPv4-compatible IPv6, ::a.b.c.d. Deprecated but still parsed and
	// still routable on stacks that accept it. The leading octet of a
	// real IPv4 address is never zero, which distinguishes this from ::1
	// and :: themselves.
	allZero := true
	for _, c := range b[:12] {
		if c != 0 {
			allZero = false
			break
		}
	}
	if allZero && b[12] != 0 {
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	}

	return netip.Addr{}, false
}

// blocked reports why addr is disallowed, or "" if it is fine.
func (g *Guard) blocked(addr netip.Addr) string {
	addr = addr.Unmap()

	// An IPv6 address carrying an IPv4 address inside it is judged by
	// what it actually reaches.
	if v4, ok := embeddedIPv4(addr); ok {
		if reason := g.blocked(v4); reason != "" {
			return reason + " (reached through an IPv6 translation address)"
		}
	}

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
	case addr == limitedBroadcast:
		return "limited broadcast address"
	}
	return ""
}

// resolveTimeout bounds a single name lookup. A caller that supplies no
// deadline of its own still gets one: an unanswered lookup is a normal
// failure mode (an expired domain, a partitioned network, a misconfigured
// internal resolver) and must not be able to park a goroutine forever.
const resolveTimeout = 5 * time.Second

// ambiguousNumericHost reports whether host looks like an attempt to write
// a numeric address in a non-canonical form: octal (0177.0.0.1), a bare
// 32-bit integer (2130706433), hexadecimal (0x7f000001), or a shortened
// dotted form (127.1).
//
// These matter because whether they resolve, and to what, is decided by
// whichever resolver the binary was built against. glibc's getaddrinfo
// accepts all of them and yields 127.0.0.1; Go's built-in resolver, which
// is what a CGO_ENABLED=0 build for the Raspberry Pi uses, does not. A
// guard whose behaviour depends on the C library it was linked against is
// not a guard, so Beacon refuses the ambiguous forms outright and requires
// a canonical address instead. No real hostname is affected: a DNS label
// that is entirely numeric cannot be a top-level domain.
func ambiguousNumericHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return false // already canonical
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		lower := strings.ToLower(l)
		if strings.HasPrefix(lower, "0x") {
			return true // hexadecimal octet
		}
	}
	// A final label of pure digits means the whole host was meant as a
	// number, not a name.
	last := labels[len(labels)-1]
	if last == "" {
		return false
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CheckURL validates a target URL before any network activity: scheme must
// be http or https, and every address the host resolves to must pass the
// range check. This is a pre-flight check only: DialContext repeats the
// address check at dial time, because DNS can change between CheckURL and
// the actual connection (DNS rebinding).
//
// It takes a context because it performs a name lookup. Resolving with a
// background context instead would put the lookup outside the reach of the
// caller's cancellation: the scheduler runs one goroutine per target and
// cancels that goroutine's context when the target is deleted or the hub
// shuts down, and a lookup that ignored it would keep the goroutine alive
// past both, blocking shutdown indefinitely.
func (g *Guard) CheckURL(ctx context.Context, raw string) error {
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
	if ambiguousNumericHost(host) {
		return fmt.Errorf("address %q is not a canonical form; write it as a plain dotted address", host)
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
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
	if ambiguousNumericHost(host) {
		return nil, fmt.Errorf("address %q is not a canonical form", host)
	}

	ip, err := netip.ParseAddr(host)
	if err != nil {
		// http.Transport normally hands DialContext a hostname, not an IP
		// literal — resolution happens here, at dial time, which is what
		// closes the DNS-rebinding window: the IP checked below is the
		// exact IP dialed immediately after, not a name checked earlier.
		lookupCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
		defer cancel()
		ips, lookupErr := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", host)
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
