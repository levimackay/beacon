// Command beaconhub is the Beacon hub: the process that collects metrics,
// checks websites, tracks incidents, and serves the API the Mac app and the
// CLI read from.
//
// It listens on loopback only. Remote access is Tailscale's job, via
// `tailscale serve` in front of this listener, not this process binding a
// routable interface.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/host"

	"github.com/levimackay/beacon/internal/api"
	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/config"
	"github.com/levimackay/beacon/internal/incident"
	"github.com/levimackay/beacon/internal/launchd"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/scheduler"
	"github.com/levimackay/beacon/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// localHostTargetID is the well-known id of the host target seeded on first
// run, so the hub always has something to report rather than starting empty.
const localHostTargetID = "host-local"

const usage = `beaconhub - the Beacon monitoring hub

Usage:
  beaconhub              run the hub in the foreground
  beaconhub install      install and start the hub as a login agent
  beaconhub uninstall    stop and remove the login agent
  beaconhub status       report whether the login agent is installed
  beaconhub version      print the version

Environment:
  BEACON_DIR    override the support directory
  BEACON_PORT   override the loopback port (default 47654)
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "", "run":
		return serve()
	case "install":
		return install()
	case "uninstall":
		return uninstall()
	case "status":
		return agentStatus()
	case "version":
		fmt.Println("beaconhub", version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "beaconhub: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func serve() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub:", err)
		return 1
	}
	log := config.NewLogger(os.Stderr, os.Getenv("BEACON_DEBUG") != "")

	c := clock.Real()
	st, err := store.Open(cfg.DBPath, c)
	if err != nil {
		log.Error("opening the database", "err", err)
		return 1
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := seedLocalHost(ctx, st); err != nil {
		log.Error("seeding the local host target", "err", err)
		return 1
	}

	machine := incident.NewMachine(c)
	if err := restoreIncidentState(ctx, st, machine); err != nil {
		log.Error("restoring incident state", "err", err)
		return 1
	}

	sched := scheduler.New(scheduler.Deps{
		Store: st,
		Clock: c,
		Collectors: map[protocol.TargetKind]collect.Collector{
			protocol.KindHost:    collect.NewHost(c),
			protocol.KindWebsite: collect.NewWeb(c, collect.NewGuard()),
		},
		Machine:    machine,
		Thresholds: incident.DefaultThresholds(),
		Logger:     log,
	})

	handler := api.NewServer(api.Deps{
		Store:     st,
		Clock:     c,
		Token:     cfg.Token,
		Hub:       hubInfo(c),
		Guard:     collect.NewGuard(),
		Scheduler: sched,
		Logger:    log,
	})

	// Listen before announcing, so a port already in use is reported as a
	// startup failure rather than a hub that appears to start and then
	// silently serves nothing.
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		log.Error("listening", "addr", cfg.Addr(), "err", err)
		return 1
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		if err := sched.Run(ctx); err != nil {
			log.Error("scheduler stopped", "err", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	log.Info("beacon hub listening",
		"addr", cfg.Addr(), "version", version, "database", cfg.DBPath)

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-serveErr:
		if err != nil {
			log.Error("http server stopped", "err", err)
			stop()
			<-schedDone
			return 1
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
	<-schedDone
	return 0
}

// seedLocalHost makes sure the machine the hub runs on is itself monitored.
// A hub with no targets would answer "unknown" to the only question the user
// cares about, so the first thing it can always report on is its own host.
func seedLocalHost(ctx context.Context, st store.Store) error {
	targets, err := st.Targets(ctx)
	if err != nil {
		return err
	}
	for _, t := range targets {
		if t.ID == localHostTargetID {
			return nil
		}
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "This " + runtime.GOOS
	}
	return st.UpsertTarget(ctx, protocol.Target{
		ID:              localHostTargetID,
		Kind:            protocol.KindHost,
		Name:            name,
		IntervalSeconds: 15,
		Enabled:         true,
	})
}

// restoreIncidentState primes the incident machine with the states that were
// already confirmed when the hub last stopped. Without this a restart would
// see every target as unknown, and the first healthy check would look like a
// recovery from an outage that never happened.
func restoreIncidentState(ctx context.Context, st store.Store, m *incident.Machine) error {
	open, err := st.OpenIncidents(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, in := range open {
		// Two observations, because a confirmed state is what was
		// persisted: replaying it once would leave it merely pending.
		m.Observe(in.TargetID, in.State, in.Summary, now)
		m.Observe(in.TargetID, in.State, in.Summary, now)
	}
	return nil
}

func hubInfo(c clock.Clock) protocol.HubInfo {
	info := protocol.HubInfo{
		Version:   version,
		StartedAt: c.Now(),
		OS:        runtime.GOOS,
	}
	if h, err := host.Info(); err == nil {
		info.Host = h.Hostname
		info.OS = h.Platform + " " + h.PlatformVersion
		info.Kernel = h.KernelVersion
	}
	if info.Host == "" {
		info.Host, _ = os.Hostname()
	}
	return info
}

func install() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub:", err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub: locating this executable:", err)
		return 1
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub: resolving this executable:", err)
		return 1
	}

	port := 0
	if cfg.Port != config.DefaultPort {
		port = cfg.Port
	}
	err = launchd.Install(context.Background(), launchd.Config{
		BinaryPath: exe,
		SupportDir: cfg.Dir,
		Port:       port,
	})
	if errors.Is(err, launchd.ErrUnsupported) {
		fmt.Fprintln(os.Stderr, "beaconhub: installing as a login agent is only supported on macOS.")
		fmt.Fprintln(os.Stderr, "On Linux, run beaconhub under systemd instead.")
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub:", err)
		return 1
	}
	fmt.Println("Beacon hub installed and running. It will start again at login.")
	fmt.Println("Check it with: beacon status")
	return 0
}

func uninstall() int {
	err := launchd.Uninstall(context.Background())
	if errors.Is(err, launchd.ErrUnsupported) {
		fmt.Fprintln(os.Stderr, "beaconhub: login agents are only supported on macOS.")
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub:", err)
		return 1
	}
	fmt.Println("Beacon hub stopped and removed. Your data is untouched.")
	return 0
}

func agentStatus() int {
	installed, err := launchd.Installed()
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub:", err)
		return 1
	}
	if !installed {
		fmt.Println("The Beacon hub is not installed as a login agent.")
		fmt.Println("Install it with: beaconhub install")
		return 1
	}
	running, err := launchd.Running(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "beaconhub:", err)
		return 1
	}
	if running {
		fmt.Println("The Beacon hub is installed and running.")
		return 0
	}
	fmt.Println("The Beacon hub is installed but not currently running.")
	return 1
}
