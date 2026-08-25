package collect

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

// Carrier-grade NAT is where Tailscale addresses live. Leaving it unblocked
// while refusing a plain 192.168 address would be an accidental hole rather
// than a decision, so it is refused by default like any other private range.
func TestGuardBlocksCarrierGradeNAT(t *testing.T) {
	g := NewGuard()
	for _, addr := range []string{"100.64.0.1", "100.100.100.100", "100.127.255.254"} {
		if reason := g.blocked(netip.MustParseAddr(addr)); reason == "" {
			t.Errorf("%s was allowed; carrier-grade NAT must be refused by default", addr)
		}
	}
	// Just outside the range, and must stay reachable.
	for _, addr := range []string{"100.63.255.255", "100.128.0.1"} {
		if reason := g.blocked(netip.MustParseAddr(addr)); reason != "" {
			t.Errorf("%s was refused as %q but is a public address", addr, reason)
		}
	}
}

// AllowPrivate is the operator saying "this target is on my own network".
// It must open up their LAN and tailnet without opening the one address
// whose exposure turns an SSRF into stolen cloud credentials.
func TestAllowPrivateNeverReachesCloudMetadata(t *testing.T) {
	g := &Guard{AllowPrivate: true}

	if reason := g.blocked(netip.MustParseAddr("169.254.169.254")); reason == "" {
		t.Fatal("AllowPrivate exposed the cloud metadata address")
	}
	if err := g.CheckURL(context.Background(), "http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("CheckURL allowed the metadata endpoint under AllowPrivate")
	}
	if _, err := g.DialContext(context.Background(), "tcp", "169.254.169.254:80"); err == nil {
		t.Fatal("DialContext allowed the metadata endpoint under AllowPrivate")
	}

	// The ranges it is supposed to open really do open.
	for _, addr := range []string{"127.0.0.1", "192.168.1.10", "10.0.0.5", "100.100.100.100"} {
		if reason := g.blocked(netip.MustParseAddr(addr)); reason != "" {
			t.Errorf("AllowPrivate should permit %s, refused as %q", addr, reason)
		}
	}
}

// An IPv4-mapped IPv6 address must not be a way around the range checks.
func TestGuardUnmapsIPv4MappedAddresses(t *testing.T) {
	g := NewGuard()
	for _, addr := range []string{"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1", "::ffff:100.64.0.1"} {
		if reason := g.blocked(netip.MustParseAddr(addr)); reason == "" {
			t.Errorf("%s slipped through as an IPv4-mapped address", addr)
		}
	}
}

func TestGuardRejectionNamesTheReasonWithoutLeakingTheTarget(t *testing.T) {
	err := NewGuard().CheckURL(context.Background(), "http://10.1.2.3/admin?token=secret-value")
	if err == nil {
		t.Fatal("private address was allowed")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("guard error leaked the query string: %v", err)
	}
	if !strings.Contains(err.Error(), "private") {
		t.Fatalf("guard error does not say why: %v", err)
	}
}
