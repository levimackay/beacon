package collect

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

// An IPv6 address that carries an IPv4 address inside it reaches that IPv4
// destination on any network implementing the translation. netip's Unmap
// folds only the IPv4-mapped form, so every other form was previously an
// unguarded route to exactly the addresses the guard exists to refuse.
// 64:ff9b::/96 is the well-known NAT64 prefix and is reachable on any
// DNS64/NAT64 network.
func TestSecurity_IPv6TranslationAddressesAreJudgedByWhatTheyReach(t *testing.T) {
	cases := []struct {
		addr string
		what string
	}{
		{"::ffff:169.254.169.254", "cloud metadata, IPv4-mapped"},
		{"64:ff9b::a9fe:a9fe", "cloud metadata, NAT64 well-known prefix"},
		{"64:ff9b:1::a9fe:a9fe", "cloud metadata, NAT64 local-use prefix"},
		{"2002:a9fe:a9fe::", "cloud metadata, 6to4"},
		{"::169.254.169.254", "cloud metadata, IPv4-compatible"},

		{"::ffff:127.0.0.1", "loopback, IPv4-mapped"},
		{"64:ff9b::7f00:1", "loopback, NAT64"},
		{"2002:7f00:1::", "loopback, 6to4"},
		{"::127.0.0.1", "loopback, IPv4-compatible"},

		{"64:ff9b::a00:1", "private 10.0.0.1, NAT64"},
		{"2002:c0a8:101::", "private 192.168.1.1, 6to4"},
		{"::10.0.0.1", "private 10.0.0.1, IPv4-compatible"},
		{"64:ff9b::6440:1", "carrier-grade NAT 100.64.0.1, NAT64"},
	}

	g := NewGuard()
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			addr := netip.MustParseAddr(c.addr)
			if reason := g.blocked(addr); reason == "" {
				t.Fatalf("%s (%s) was allowed", c.addr, c.what)
			}
		})
	}
}

// The metadata endpoint must stay refused through a translation address
// even for a target the operator opted into private access for.
func TestSecurity_TranslatedMetadataRefusedEvenWithPrivateOptIn(t *testing.T) {
	g := &Guard{AllowPrivate: true}
	for _, addr := range []string{
		"64:ff9b::a9fe:a9fe",
		"2002:a9fe:a9fe::",
		"::169.254.169.254",
		"::ffff:169.254.169.254",
	} {
		if reason := g.blocked(netip.MustParseAddr(addr)); reason == "" {
			t.Errorf("%s reached the metadata endpoint under AllowPrivate", addr)
		}
	}
}

// Ordinary IPv6 addresses must remain reachable: the translation checks
// must not turn the guard into a blanket IPv6 refusal.
func TestSecurity_OrdinaryIPv6RemainsReachable(t *testing.T) {
	g := NewGuard()
	for _, addr := range []string{
		"2606:4700:4700::1111", // a public resolver
		"2001:4860:4860::8888", // another
		"2400:cb00:2048:1::1",  // a public host
	} {
		if reason := g.blocked(netip.MustParseAddr(addr)); reason != "" {
			t.Errorf("public address %s was refused as %q", addr, reason)
		}
	}
}

// A 6to4 address whose embedded value is itself public must stay allowed,
// proving the check inspects the embedded address rather than refusing the
// prefix wholesale.
func TestSecurity_TranslationPrefixWithPublicEmbeddedAddressIsAllowed(t *testing.T) {
	// 2002:0808:0808:: carries 8.8.8.8.
	if reason := NewGuard().blocked(netip.MustParseAddr("2002:808:808::")); reason != "" {
		t.Fatalf("6to4 carrying a public address was refused as %q", reason)
	}
}

// Whether a non-canonical numeric address resolves, and to what, depends on
// the resolver the binary was linked against: glibc's getaddrinfo accepts
// octal and integer forms, Go's built-in resolver does not. A static
// CGO_ENABLED=0 build for the Raspberry Pi uses the latter. Beacon refuses
// the ambiguous forms so its behaviour does not depend on that difference.
func TestSecurity_AmbiguousNumericHostsAreRefusedRegardlessOfResolver(t *testing.T) {
	refused := []string{
		"http://0177.0.0.1/",  // octal
		"http://2130706433/",  // 32-bit integer
		"http://0x7f000001/",  // hexadecimal
		"http://127.1/",       // shortened dotted
		"http://0x7f.0.0.1/",  // mixed hex octet
		"http://010.0.0.1/",   // octal first octet
		"http://192.168.257/", // shortened, out of range octet
		"http://2852039166/",  // integer for 169.254.169.254
	}
	allowedShape := []string{
		"http://example.com/",
		"http://sub.example.com/",
		"http://example.com./", // trailing dot is a legitimate FQDN form
	}

	for _, raw := range refused {
		t.Run(raw, func(t *testing.T) {
			err := NewGuard().CheckURL(context.Background(), raw)
			if err == nil {
				t.Fatalf("%s was accepted; its meaning depends on the resolver", raw)
			}
		})
	}
	for _, raw := range allowedShape {
		t.Run(raw, func(t *testing.T) {
			// These may fail to resolve in a sandbox, which is fine.
			// What must not happen is a refusal for being numeric.
			err := NewGuard().CheckURL(context.Background(), raw)
			if err != nil && !isResolutionFailure(err) {
				t.Fatalf("legitimate hostname %s was refused: %v", raw, err)
			}
		})
	}
}

func isResolutionFailure(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) || errors.Is(err, ErrUnresolvable)
}

// The dial-time gate must refuse the ambiguous forms too, since it is the
// last check before a connection is made.
func TestSecurity_DialRefusesAmbiguousNumericHosts(t *testing.T) {
	g := NewGuard()
	for _, addr := range []string{"0177.0.0.1:80", "2130706433:80", "0x7f000001:80"} {
		if _, err := g.DialContext(context.Background(), "tcp", addr); err == nil {
			t.Errorf("DialContext accepted %s", addr)
		}
	}
}
