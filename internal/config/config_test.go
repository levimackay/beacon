package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGeneratesTokenOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEACON_DIR", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Token) < 32 {
		t.Fatalf("token is suspiciously short: %d chars", len(c.Token))
	}
	if c.Addr() != "127.0.0.1:47654" {
		t.Fatalf("Addr = %q, want loopback default", c.Addr())
	}

	info, err := os.Stat(c.TokenPath)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token mode = %o, want 600", perm)
	}

	again, err := Load()
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if again.Token != c.Token {
		t.Fatal("Load minted a new token instead of reusing the existing one")
	}
}

func TestLoadTightensLooseTokenPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEACON_DIR", dir)
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("existing-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Token != "existing-token" {
		t.Fatalf("token = %q, want existing-token", c.Token)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("world-readable token was not tightened: mode %o", perm)
	}
}

func TestLoadClientDoesNotMintTokens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEACON_DIR", dir)

	if _, err := LoadClient(); err != ErrNotConfigured {
		t.Fatalf("LoadClient on a fresh machine = %v, want ErrNotConfigured", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "token")); err == nil {
		t.Fatal("LoadClient created a token file; only the hub may mint credentials")
	}

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadClient(); err != nil {
		t.Fatalf("LoadClient after hub setup: %v", err)
	}
}

func TestPortOverrideRejectsGarbage(t *testing.T) {
	t.Setenv("BEACON_DIR", t.TempDir())
	for _, bad := range []string{"nope", "0", "-1", "70000"} {
		t.Setenv("BEACON_PORT", bad)
		if _, err := Load(); err == nil {
			t.Fatalf("BEACON_PORT=%q was accepted", bad)
		}
	}
	t.Setenv("BEACON_PORT", "1234")
	c, err := Load()
	if err != nil {
		t.Fatalf("valid port rejected: %v", err)
	}
	if c.Addr() != "127.0.0.1:1234" {
		t.Fatalf("Addr = %q", c.Addr())
	}
}

func TestLoggerRedactsCredentials(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, false)
	log.Info("starting",
		slog.String("token", "super-secret-value"),
		slog.String("Authorization", "Bearer super-secret-value"),
		slog.String("api_key", "another-secret"),
		slog.String("host", "mac"),
	)
	out := buf.String()
	if strings.Contains(out, "super-secret-value") || strings.Contains(out, "another-secret") {
		t.Fatalf("logger leaked a credential:\n%s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected redaction marker:\n%s", out)
	}
	if !strings.Contains(out, "mac") {
		t.Fatalf("redaction ate a harmless field:\n%s", out)
	}
}
