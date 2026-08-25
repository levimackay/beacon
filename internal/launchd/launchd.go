// Package launchd installs the Beacon hub as a per-user launchd agent, so
// monitoring keeps running after the app is quit and starts again at login.
//
// It is a user agent, not a system daemon: it lives in ~/Library/LaunchAgents,
// runs as the logged-in user, and needs no root, no sudo and no installer
// package. That is a deliberate trade. A system daemon would keep running
// with the user logged out, but it would require an authenticated privileged
// install, which is exactly the kind of setup friction Beacon exists to avoid.
// When the hub moves to the Raspberry Pi, systemd takes over this role and
// the always-on question stops being the Mac's problem.
package launchd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Label is the launchd job label and the plist filename stem.
const Label = "com.levimackay.beacon.hub"

// launchctlPath is an absolute path on purpose. Resolving "launchctl" through
// PATH would let a directory earlier in the user's PATH decide what Beacon
// executes during install.
const launchctlPath = "/bin/launchctl"

// ErrUnsupported is returned on platforms that have no launchd.
var ErrUnsupported = errors.New("launchd is only available on macOS")

// PlistPath is where the agent definition lives for the current user.
func PlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// Config describes the agent to be written.
type Config struct {
	// BinaryPath is the absolute path to the beaconhub executable.
	BinaryPath string
	// SupportDir receives the agent's stdout and stderr logs.
	SupportDir string
	// Port, when non-zero, is passed through as BEACON_PORT so a hub
	// installed on a custom port keeps it across restarts.
	Port int
}

// Plist renders the launchd property list for the given configuration.
//
// KeepAlive is set to restart the job only on abnormal exit, so a hub that
// stops cleanly (during an uninstall, say) is not immediately resurrected,
// while one that crashes comes back. ThrottleInterval keeps a hub that is
// crash-looping from burning the machine's CPU.
func Plist(c Config) ([]byte, error) {
	if !filepath.IsAbs(c.BinaryPath) {
		return nil, fmt.Errorf("binary path %q must be absolute", c.BinaryPath)
	}
	if !filepath.IsAbs(c.SupportDir) {
		return nil, fmt.Errorf("support directory %q must be absolute", c.SupportDir)
	}

	env := ""
	if c.Port != 0 {
		env = fmt.Sprintf(`
	<key>EnvironmentVariables</key>
	<dict>
		<key>BEACON_PORT</key>
		<string>%s</string>
	</dict>`, escape(strconv.Itoa(c.Port)))
	}

	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>%s
</dict>
</plist>
`,
		escape(Label),
		escape(c.BinaryPath),
		escape(filepath.Join(c.SupportDir, "hub.log")),
		escape(filepath.Join(c.SupportDir, "hub.err.log")),
		env,
	)
	return []byte(body), nil
}

// escape XML-encodes a value destined for a plist string. Paths can contain
// ampersands and angle brackets, and a home directory name is not something
// Beacon controls.
func escape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return ""
	}
	return buf.String()
}

// Install writes the plist and loads the agent. It is idempotent: an already
// installed agent is unloaded and reloaded so the new definition takes effect.
func Install(ctx context.Context, c Config) error {
	if runtime.GOOS != "darwin" {
		return ErrUnsupported
	}
	path, err := PlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	data, err := Plist(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	// A previous definition may still be loaded. Failure here is expected
	// on a first install and is not an error.
	_ = bootout(ctx)

	if _, err := launchctl(ctx, "bootstrap", domain(), path); err != nil {
		return fmt.Errorf("loading the Beacon agent: %w", err)
	}
	return nil
}

// Uninstall stops the agent and removes its definition.
func Uninstall(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return ErrUnsupported
	}
	path, err := PlistPath()
	if err != nil {
		return err
	}
	// Unload first: removing the plist of a loaded job leaves launchd
	// holding a definition that no longer exists on disk.
	_ = bootout(ctx)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// Installed reports whether the agent definition exists on disk.
func Installed() (bool, error) {
	path, err := PlistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Running reports whether launchd currently has the job loaded.
func Running(ctx context.Context) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, ErrUnsupported
	}
	out, err := launchctl(ctx, "print", domain()+"/"+Label)
	if err != nil {
		// `launchctl print` exits non-zero when the job is not loaded,
		// which is an answer rather than a failure.
		return false, nil
	}
	return strings.Contains(out, Label), nil
}

func bootout(ctx context.Context) error {
	_, err := launchctl(ctx, "bootout", domain()+"/"+Label)
	return err
}

// domain is the per-user GUI domain launchd scopes the agent to.
func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// launchctl runs the launchd control tool with a fixed absolute path and a
// fixed argument list. There is no shell involved and no user-supplied string
// reaches an argument except paths this package constructs itself. This is
// the only place in Beacon that executes a subprocess, and it is reachable
// only from an explicit install or uninstall, never from the API.
func launchctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, launchctlPath, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}
