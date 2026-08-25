package collect

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func TestGuard_CheckURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"loopback ipv4", "http://127.0.0.1/", true},
		{"loopback hostname", "http://localhost/", true},
		{"loopback ipv6", "http://[::1]/", true},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"private class a", "http://10.0.0.1/", true},
		{"private class c", "http://192.168.1.1/", true},
		{"private class b", "http://172.16.0.1/", true},
		{"unique local ipv6", "http://[fd00::1]/", true},
		{"unspecified", "http://0.0.0.0/", true},
		{"file scheme", "file:///etc/passwd", true},
		{"gopher scheme", "gopher://x/", true},
		{"public address", "http://8.8.8.8/", false},
	}

	g := NewGuard()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := g.CheckURL(context.Background(), tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("CheckURL(%q): want error, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("CheckURL(%q): want no error, got %v", tc.url, err)
			}
		})
	}
}

// TestGuard_RejectsByResolvedIPNotHostname proves the guard rejects a
// hostname that is not "localhost" or any other recognizable string, based
// solely on where it resolves. It runs a tiny local DNS server (loopback
// only, no internet access) that answers every A query with 127.0.0.1, and
// points the resolver at it by swapping net.DefaultResolver for the
// duration of the test — the same resolver Guard.CheckURL uses internally.
func TestGuard_RejectsByResolvedIPNotHostname(t *testing.T) {
	addr, stop := startFakeDNS(t, net.IPv4(127, 0, 0, 1))
	defer stop()

	restore := swapDefaultResolver(&net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", addr)
		},
	})
	defer restore()

	const host = "totally-innocuous-name.example.internal"
	err := NewGuard().CheckURL(context.Background(), "http://"+host+"/")
	if err == nil {
		t.Fatalf("CheckURL(%s): want rejection (resolves to loopback), got nil", host)
	}
	if strings.Contains(err.Error(), host) {
		t.Fatalf("rejection message %q references the hostname string; it must be based on the resolved IP", err.Error())
	}
}

func TestGuard_AllowPrivate(t *testing.T) {
	g := &Guard{AllowPrivate: true}
	if err := g.CheckURL(context.Background(), "http://127.0.0.1:9/"); err != nil {
		t.Fatalf("AllowPrivate should bypass range check: %v", err)
	}
	if err := g.CheckURL(context.Background(), "ftp://127.0.0.1/"); err == nil {
		t.Fatal("AllowPrivate must not bypass the scheme check")
	}
}

func TestGuard_DialContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	g := NewGuard()
	if _, err := g.DialContext(context.Background(), "tcp", ln.Addr().String()); err == nil {
		t.Fatal("DialContext to loopback should be rejected without AllowPrivate")
	}

	g.AllowPrivate = true
	conn, err := g.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialContext with AllowPrivate: %v", err)
	}
	conn.Close()
}

// swapDefaultResolver replaces net.DefaultResolver and returns a func that
// restores the original.
func swapDefaultResolver(r *net.Resolver) (restore func()) {
	old := net.DefaultResolver
	net.DefaultResolver = r
	return func() { net.DefaultResolver = old }
}

// startFakeDNS starts a minimal UDP DNS server on loopback that answers
// every A query with ip and every AAAA query with no answers. It is enough
// wire format to satisfy Go's pure-Go resolver and nothing more.
func startFakeDNS(t *testing.T, ip net.IP) (addr string, stop func()) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ip4 := ip.To4()

	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := buildDNSResponse(buf[:n], ip4)
			if resp != nil {
				_, _ = conn.WriteTo(resp, from)
			}
		}
	}()

	return conn.LocalAddr().String(), func() { conn.Close() }
}

// buildDNSResponse answers a single-question query. For an A query it
// returns one answer record; for anything else (AAAA included) it returns
// NOERROR with zero answers, which is enough for LookupNetIP to fall back
// to whichever query type does carry an answer.
func buildDNSResponse(query []byte, ip4 []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	id := query[0:2]

	// Walk the question's QNAME to find where it ends.
	i := 12
	for i < len(query) && query[i] != 0 {
		i += int(query[i]) + 1
	}
	if i >= len(query) {
		return nil
	}
	qend := i + 1 + 4 // null label + QTYPE(2) + QCLASS(2)
	if qend > len(query) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(query[i+1 : i+3])
	question := query[12:qend]

	var ancount uint16
	var answer []byte
	if qtype == 1 && ip4 != nil { // A record
		ancount = 1
		answer = append(answer, 0xC0, 0x0C)             // pointer to name at offset 12
		answer = append(answer, 0x00, 0x01)             // TYPE A
		answer = append(answer, 0x00, 0x01)             // CLASS IN
		answer = append(answer, 0x00, 0x00, 0x00, 0x3C) // TTL 60
		answer = append(answer, 0x00, 0x04)             // RDLENGTH
		answer = append(answer, ip4...)
	}

	resp := make([]byte, 0, 12+len(question)+len(answer))
	resp = append(resp, id...)
	resp = append(resp, 0x81, 0x80) // standard response, recursion available
	resp = append(resp, 0x00, 0x01) // QDCOUNT=1
	resp = append(resp, byte(ancount>>8), byte(ancount))
	resp = append(resp, 0x00, 0x00) // NSCOUNT
	resp = append(resp, 0x00, 0x00) // ARCOUNT
	resp = append(resp, question...)
	resp = append(resp, answer...)
	return resp
}
