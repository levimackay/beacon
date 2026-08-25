// Package config resolves Beacon's on-disk locations and its local API
// credential. Beacon has no configuration file: everything here is either
// derived from the platform or generated on first run.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// DefaultPort is the loopback port the hub listens on. It sits in the
// IANA dynamic range and is not registered to anything.
const DefaultPort = 47654

// Config is the resolved runtime environment of a hub or client process.
type Config struct {
	Dir       string
	DBPath    string
	TokenPath string
	Token     string
	Port      int
}

// Addr is the listen address. It is always loopback. Beacon deliberately
// offers no way to bind a routable interface: remote access is Tailscale's
// job, via `tailscale serve` in front of this listener.
func (c *Config) Addr() string { return fmt.Sprintf("127.0.0.1:%d", c.Port) }

// BaseURL is the address a local client should dial.
func (c *Config) BaseURL() string { return "http://" + c.Addr() }

// Dir returns Beacon's support directory for the current platform.
// BEACON_DIR overrides it, which exists so tests and parallel dev instances
// do not fight over one database.
func supportDir() (string, error) {
	if d := os.Getenv("BEACON_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Beacon"), nil
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "beacon"), nil
	}
	return filepath.Join(home, ".local", "state", "beacon"), nil
}

func port() (int, error) {
	raw := os.Getenv("BEACON_PORT")
	if raw == "" {
		return DefaultPort, nil
	}
	p, err := strconv.Atoi(raw)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("BEACON_PORT %q is not a valid port", raw)
	}
	return p, nil
}

// Load resolves the configuration, creating the support directory and
// generating an API token on first run. The token file is written 0600 and
// the directory 0700.
func Load() (*Config, error) {
	dir, err := supportDir()
	if err != nil {
		return nil, err
	}
	p, err := port()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	c := &Config{
		Dir:       dir,
		DBPath:    filepath.Join(dir, "beacon.db"),
		TokenPath: filepath.Join(dir, "token"),
		Port:      p,
	}
	c.Token, err = loadOrCreateToken(c.TokenPath)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// LoadClient resolves configuration for a client process. It never creates
// anything: a client that finds no token should say the hub is not set up
// rather than mint a credential the hub does not know about.
func LoadClient() (*Config, error) {
	dir, err := supportDir()
	if err != nil {
		return nil, err
	}
	p, err := port()
	if err != nil {
		return nil, err
	}
	c := &Config{
		Dir:       dir,
		DBPath:    filepath.Join(dir, "beacon.db"),
		TokenPath: filepath.Join(dir, "token"),
		Port:      p,
	}
	b, err := os.ReadFile(c.TokenPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	c.Token = strings.TrimSpace(string(b))
	if c.Token == "" {
		return nil, ErrNotConfigured
	}
	return c, nil
}

// ErrNotConfigured means no hub token exists yet on this machine.
var ErrNotConfigured = errors.New("beacon is not set up on this machine yet")

func loadOrCreateToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		tok := strings.TrimSpace(string(b))
		if tok == "" {
			break
		}
		if err := enforceTokenPermissions(path); err != nil {
			return "", err
		}
		return tok, nil
	case errors.Is(err, fs.ErrNotExist):
	default:
		return "", fmt.Errorf("reading token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing token: %w", err)
	}
	return tok, nil
}

// enforceTokenPermissions tightens a token file that has become group or
// world readable, rather than trusting a mode someone else set.
func enforceTokenPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat token: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("tightening token permissions: %w", err)
		}
	}
	return nil
}
