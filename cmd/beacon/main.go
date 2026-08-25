// Command beacon is the CLI for a Beacon hub: the two-second glance at
// whether everything being watched is okay, plus target management.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/levimackay/beacon/internal/cliclient"
	"github.com/levimackay/beacon/internal/config"
	"github.com/levimackay/beacon/internal/protocol"
)

const cliVersion = "0.1.0-dev"

const usageText = `Usage: beacon <command> [flags]

Commands:
  status                                the two-second glance (default)
  devices                                host targets only
  websites                               website targets only
  services                               service targets only
  incidents [--since 24h] [--limit 50] [--target ID]
  add <url> --name NAME [--every 60s] [--expect 200] [--private]
                                        [--contains TEXT] [--warn-after 2s]
  rm <id>
  diagnostics                            troubleshooting view
  version                                print the CLI version

Global flags (accepted on every command):
  --json                                 emit the raw API payload instead of rendered text
  --no-color                             disable colored output

Beacon hub not running? Start it with: beaconhub
`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, defaultNewClient))
}

func defaultNewClient() (*cliclient.Client, error) {
	cfg, err := config.LoadClient()
	if err != nil {
		return nil, err
	}
	return cliclient.New(cfg), nil
}

// run executes one CLI invocation and returns the process exit code. It
// never panics: any unexpected failure is converted to a short message on
// stderr and exit code 1.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, newClient func() (*cliclient.Client, error)) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "beacon: internal error: %v\n", r)
			code = 1
		}
	}()

	rest, jsonOut, noColorFlag := splitGlobalFlags(args)
	if len(rest) == 0 {
		rest = []string{"status"}
	}
	cmd, cmdArgs := rest[0], rest[1:]

	switch cmd {
	case "--help", "-help", "-h", "help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "version":
		if jsonOut {
			fmt.Fprintf(stdout, "{\"version\":%q}\n", cliVersion)
		} else {
			fmt.Fprintf(stdout, "beacon %s\n", cliVersion)
		}
		return 0
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	ren := cliclient.Renderer{Color: colorEnabled(noColorFlag), Now: time.Now()}

	// getClient centralizes the "no client, no command" failure path so
	// every branch below can bail out the same way.
	getClient := func() (*cliclient.Client, int, bool) {
		cl, err := newClient()
		if err != nil {
			return nil, handleErr(stderr, err), false
		}
		return cl, 0, true
	}

	switch cmd {
	case "status":
		cl, ec, ok := getClient()
		if !ok {
			return ec
		}
		if jsonOut {
			b, err := cl.Raw(ctx, "/v1/snapshot")
			if err != nil {
				return handleErr(stderr, err)
			}
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		snap, err := cl.Snapshot(ctx)
		if err != nil {
			return handleErr(stderr, err)
		}
		ren.Status(stdout, snap)
		return 0

	case "devices", "websites", "services":
		cl, ec, ok := getClient()
		if !ok {
			return ec
		}
		targets, err := cl.Targets(ctx)
		if err != nil {
			return handleErr(stderr, err)
		}
		kind := map[string]protocol.TargetKind{
			"devices":  protocol.KindHost,
			"websites": protocol.KindWebsite,
			"services": protocol.KindService,
		}[cmd]
		filtered := targets[:0]
		for _, t := range targets {
			if t.Kind == kind {
				filtered = append(filtered, t)
			}
		}
		if jsonOut {
			return writeJSON(stdout, filtered)
		}
		ren.Targets(stdout, filtered)
		return 0

	case "incidents":
		fs := flag.NewFlagSet("incidents", flag.ContinueOnError)
		fs.SetOutput(stderr)
		since := fs.String("since", "24h", "how far back to look, as a Go duration (e.g. 24h, 30m)")
		limit := fs.Int("limit", 50, "maximum number of incidents to show")
		target := fs.String("target", "", "only show incidents for this target id")
		if err := fs.Parse(cmdArgs); err != nil {
			return 1
		}
		var sinceTime time.Time
		if *since != "" {
			d, err := time.ParseDuration(*since)
			if err != nil {
				fmt.Fprintf(stderr, "beacon: --since must be a duration like 24h or 30m: %v\n", err)
				return 1
			}
			sinceTime = time.Now().Add(-d)
		}
		cl, ec, ok := getClient()
		if !ok {
			return ec
		}
		incs, err := cl.Incidents(ctx, sinceTime, *limit)
		if err != nil {
			return handleErr(stderr, err)
		}
		if *target != "" {
			filtered := incs[:0]
			for _, in := range incs {
				if in.TargetID == *target {
					filtered = append(filtered, in)
				}
			}
			incs = filtered
		}
		if jsonOut {
			return writeJSON(stdout, incs)
		}
		ren.Incidents(stdout, incs)
		return 0

	case "add":
		if len(cmdArgs) == 0 || strings.HasPrefix(cmdArgs[0], "-") {
			fmt.Fprintln(stderr, "beacon: add requires a URL, e.g. beacon add https://example.com --name Site")
			return 1
		}
		rawURL := cmdArgs[0]
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		fs.SetOutput(stderr)
		name := fs.String("name", "", "a short label for this target (required)")
		every := fs.String("every", "60s", "how often to check, as a Go duration (e.g. 30s, 5m); minimum 5s")
		expect := fs.Int("expect", 0, "expected HTTP status code (default: any successful status)")
		private := fs.Bool("private", false, "allow this target's address to resolve to a LAN, loopback, or Tailscale address")
		contains := fs.String("contains", "", "fail the check (report down) if the response body does not contain this text")
		warnAfter := fs.String("warn-after", "", "report warning, not down, when response latency exceeds this duration (e.g. 2s)")
		if err := fs.Parse(cmdArgs[1:]); err != nil {
			return 1
		}
		if _, err := url.ParseRequestURI(rawURL); err != nil {
			fmt.Fprintf(stderr, "beacon: %q is not a valid URL\n", rawURL)
			return 1
		}
		if *name == "" {
			fmt.Fprintln(stderr, "beacon: --name is required")
			return 1
		}
		dur, err := time.ParseDuration(*every)
		if err != nil {
			fmt.Fprintf(stderr, "beacon: --every must be a duration like 30s or 5m: %v\n", err)
			return 1
		}
		if dur < 5*time.Second {
			fmt.Fprintf(stderr, "beacon: --every must be at least 5s, got %s\n", dur)
			return 1
		}
		var warnAfterMS int
		if *warnAfter != "" {
			d, err := time.ParseDuration(*warnAfter)
			if err != nil {
				fmt.Fprintf(stderr, "beacon: --warn-after must be a duration like 2s or 500ms: %v\n", err)
				return 1
			}
			if d <= 0 {
				fmt.Fprintf(stderr, "beacon: --warn-after must be positive, got %s\n", d)
				return 1
			}
			warnAfterMS = int(d.Milliseconds())
		}
		cl, ec, ok := getClient()
		if !ok {
			return ec
		}
		t := protocol.Target{
			Kind:            protocol.KindWebsite,
			Name:            *name,
			Address:         rawURL,
			IntervalSeconds: int(dur.Seconds()),
			ExpectStatus:    *expect,
			Enabled:         true,
			AllowPrivate:    *private,
			Contains:        *contains,
			WarnAfterMS:     warnAfterMS,
		}
		if err := cl.AddTarget(ctx, t); err != nil {
			return handleErr(stderr, err)
		}
		if jsonOut {
			return writeJSON(stdout, t)
		}
		fmt.Fprintf(stdout, "Added %s (%s), checking every %s\n", t.Name, t.Address, dur)
		return 0

	case "rm":
		if len(cmdArgs) == 0 {
			fmt.Fprintln(stderr, "beacon: rm requires a target id")
			return 1
		}
		id := cmdArgs[0]
		cl, ec, ok := getClient()
		if !ok {
			return ec
		}
		if err := cl.DeleteTarget(ctx, id); err != nil {
			return handleErr(stderr, err)
		}
		fmt.Fprintf(stdout, "Removed %s\n", id)
		return 0

	case "diagnostics":
		cl, ec, ok := getClient()
		if !ok {
			return ec
		}
		if jsonOut {
			b, err := cl.Raw(ctx, "/v1/diagnostics")
			if err != nil {
				return handleErr(stderr, err)
			}
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		d, err := cl.Diagnostics(ctx)
		if err != nil {
			return handleErr(stderr, err)
		}
		ren.Diagnostics(stdout, d)
		return 0

	default:
		fmt.Fprint(stderr, usageText)
		return 2
	}
}

// splitGlobalFlags pulls --json/--no-color out of args regardless of where
// they appear, so they work whether typed before or after the subcommand.
func splitGlobalFlags(args []string) (rest []string, jsonOut, noColor bool) {
	for _, a := range args {
		switch a {
		case "--json", "-json":
			jsonOut = true
		case "--no-color", "-no-color":
			noColor = true
		default:
			rest = append(rest, a)
		}
	}
	return rest, jsonOut, noColor
}

// colorEnabled decides whether to paint output: on by default only when
// stdout is a real terminal, off if NO_COLOR is set or --no-color was
// passed.
func colorEnabled(noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// handleErr turns a client/config failure into a short human sentence on
// stderr and returns the exit code for it. This is the one place that knows
// how to talk about every documented failure mode.
func handleErr(stderr io.Writer, err error) int {
	switch {
	case errors.Is(err, config.ErrNotConfigured):
		fmt.Fprintln(stderr, "Beacon is not set up on this machine yet. Start the hub with: beaconhub")
	case errors.Is(err, cliclient.ErrUnreachable):
		url := strings.TrimPrefix(err.Error(), cliclient.ErrUnreachable.Error()+": ")
		fmt.Fprintf(stderr, "Beacon hub is not responding at %s. Is it running?\n", url)
	case errors.Is(err, cliclient.ErrUnauthorized):
		tokenPath := "the hub's token file"
		if cfg, cfgErr := config.LoadClient(); cfgErr == nil {
			tokenPath = cfg.TokenPath
		}
		fmt.Fprintf(stderr, "The hub rejected this token. The token file may be out of sync: %s\n", tokenPath)
	default:
		fmt.Fprintf(stderr, "beacon: %s\n", err)
	}
	return 1
}

func writeJSON(w io.Writer, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "beacon: encoding output: %s\n", err)
		return 1
	}
	fmt.Fprintln(w, string(b))
	return 0
}
