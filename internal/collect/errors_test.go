package collect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

// The sanitized error is the line a person reads when something is broken,
// and often the only line they read. It must be a plain phrase, must not
// carry the request URL (which can hold a query string), and must not be
// Go's wrapped-error prose.
func TestSanitizeErrProducesHumanPhrases(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "connection refused",
			err: &url.Error{Op: "Get", URL: "https://example.com/health?token=secret",
				Err: &net.OpError{Op: "dial", Net: "tcp",
					Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8899},
					Err:  syscall.ECONNREFUSED}},
			want: "connection refused",
		},
		{
			name: "connection reset",
			err:  &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{Err: syscall.ECONNRESET}},
			want: "connection reset",
		},
		{
			name: "host unreachable",
			err:  &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{Err: syscall.EHOSTUNREACH}},
			want: "host unreachable",
		},
		{
			name: "dns not found",
			err:  &url.Error{Op: "Get", URL: "https://nope.invalid", Err: &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}},
			want: "host could not be resolved",
		},
		{
			name: "server hung up",
			err:  &url.Error{Op: "Get", URL: "https://example.com", Err: io.ErrUnexpectedEOF},
			want: "the server closed the connection",
		},
		{
			name: "deadline",
			err:  &url.Error{Op: "Get", URL: "https://example.com", Err: context.DeadlineExceeded},
			want: "connection timeout",
		},
		{
			name: "redirect limit",
			err:  &url.Error{Op: "Get", URL: "https://example.com", Err: errRedirectLimit},
			want: errRedirectLimit.Error(),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeErr(c.err)
			if got != c.want {
				t.Fatalf("sanitizeErr = %q, want %q", got, c.want)
			}
		})
	}
}

// Whatever the failure, the message must never carry the request URL, since
// a monitored endpoint can legitimately have a token in its query string
// and this string is persisted and displayed.
func TestSanitizeErrNeverLeaksTheURL(t *testing.T) {
	secretURL := "https://example.com/health?token=super-secret"
	errs := []error{
		&url.Error{Op: "Get", URL: secretURL, Err: &net.OpError{Err: syscall.ECONNREFUSED}},
		&url.Error{Op: "Get", URL: secretURL, Err: context.DeadlineExceeded},
		&url.Error{Op: "Get", URL: secretURL, Err: errors.New("something unmapped")},
		&url.Error{Op: "Get", URL: secretURL, Err: &net.DNSError{Err: "no such host", IsNotFound: true}},
	}
	for i, err := range errs {
		got := sanitizeErr(err)
		if strings.Contains(got, "super-secret") || strings.Contains(got, secretURL) {
			t.Fatalf("case %d leaked the URL: %q", i, got)
		}
	}
}

// An address in an OpError's prose is noise to the reader and can disclose
// internal topology; only the innermost cause should survive.
func TestSanitizeErrDropsTheDialAddress(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://example.com",
		Err: &net.OpError{Op: "dial", Net: "tcp",
			Addr: &net.TCPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 8899},
			Err:  syscall.ECONNREFUSED}}
	got := sanitizeErr(err)
	if strings.Contains(got, "10.1.2.3") || strings.Contains(got, "8899") {
		t.Fatalf("dial address survived sanitizing: %q", got)
	}
}

func TestSanitizeErrHandlesNil(t *testing.T) {
	if got := sanitizeErr(nil); got != "" {
		t.Fatalf("sanitizeErr(nil) = %q, want empty", got)
	}
}

// An unmapped error still has to produce something, rather than an empty
// string that would render as a target failing for no stated reason.
func TestSanitizeErrFallsBackToTheMessage(t *testing.T) {
	got := sanitizeErr(fmt.Errorf("some novel transport failure"))
	if got != "some novel transport failure" {
		t.Fatalf("sanitizeErr = %q", got)
	}
}
