package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/incident"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/store"
)

// run's non-serving subcommands (version, help, unknown) are pure: they
// only print and return a code, with no store, network or filesystem side
// effects. serve/install/uninstall/status all touch real config, launchd
// or a listening socket and are left untested here for that reason.
// cmd/beaconhub had no test file at all before this one.

func TestRunVersionReturnsZero(t *testing.T) {
	if got := run([]string{"version"}); got != 0 {
		t.Fatalf("run([version]) = %d, want 0", got)
	}
}

func TestRunHelpVariantsReturnZero(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		if got := run(args); got != 0 {
			t.Fatalf("run(%v) = %d, want 0", args, got)
		}
	}
}

func TestRunUnknownCommandReturnsTwo(t *testing.T) {
	if got := run([]string{"bogus"}); got != 2 {
		t.Fatalf("run([bogus]) = %d, want 2", got)
	}
}

func openMainTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "beacon.db"), clock.Real())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSeedLocalHostCreatesExactlyOneHostTarget(t *testing.T) {
	ctx := context.Background()
	s := openMainTestStore(t)

	if err := seedLocalHost(ctx, s); err != nil {
		t.Fatalf("seedLocalHost: %v", err)
	}

	targets, err := s.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want exactly 1", targets)
	}
	got := targets[0]
	if got.ID != localHostTargetID || got.Kind != protocol.KindHost || !got.Enabled {
		t.Fatalf("seeded target = %+v, want id=%q kind=host enabled=true", got, localHostTargetID)
	}
	if got.Name == "" {
		t.Fatal("seeded target has an empty name")
	}
}

// A second call (the normal case: seedLocalHost runs on every startup) must
// not create a duplicate or otherwise change the seeded target.
func TestSeedLocalHostIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openMainTestStore(t)

	if err := seedLocalHost(ctx, s); err != nil {
		t.Fatalf("first seedLocalHost: %v", err)
	}
	if err := seedLocalHost(ctx, s); err != nil {
		t.Fatalf("second seedLocalHost: %v", err)
	}

	targets, err := s.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets after 2 seed calls = %+v, want still exactly 1", targets)
	}
}

// The guard is specifically "does the local-host id exist", not "does any
// target exist": an unrelated target must not suppress seeding.
func TestSeedLocalHostAddsAlongsideAnUnrelatedTarget(t *testing.T) {
	ctx := context.Background()
	s := openMainTestStore(t)

	if err := s.UpsertTarget(ctx, protocol.Target{
		ID: "web-1", Kind: protocol.KindWebsite, Name: "n", Address: "https://example.com",
		IntervalSeconds: 30, Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	if err := seedLocalHost(ctx, s); err != nil {
		t.Fatalf("seedLocalHost: %v", err)
	}

	targets, err := s.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %+v, want 2 (the pre-existing target plus the seeded host)", targets)
	}
	foundHost := false
	for _, tg := range targets {
		if tg.ID == localHostTargetID {
			foundHost = true
		}
	}
	if !foundHost {
		t.Fatalf("targets = %+v, want the local host target to be seeded alongside the existing one", targets)
	}
}

// restoreIncidentState must fully confirm each open incident's state in the
// machine, not merely leave it pending: it feeds the observation twice
// specifically because a single Observe only starts a pending run
// (OpenConfirmations defaults to 2). A regression here would show every
// open incident as freshly "recovering" on every hub restart.
func TestRestoreIncidentStateFullyConfirmsOpenIncidents(t *testing.T) {
	ctx := context.Background()
	s := openMainTestStore(t)
	now := time.Now()

	if _, err := s.OpenIncident(ctx, protocol.Incident{
		TargetID: "web-1", TargetName: "Site", State: protocol.StateDown, StartedAt: now, Summary: "connection refused",
	}); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	m := incident.NewMachine(clock.Real())
	if err := restoreIncidentState(ctx, s, m); err != nil {
		t.Fatalf("restoreIncidentState: %v", err)
	}

	if got := m.Current("web-1"); got != protocol.StateDown {
		t.Fatalf("Current(web-1) = %v, want down (fully confirmed, not merely pending)", got)
	}
}

func TestRestoreIncidentStateWithNoOpenIncidentsIsANoOp(t *testing.T) {
	ctx := context.Background()
	s := openMainTestStore(t)

	m := incident.NewMachine(clock.Real())
	if err := restoreIncidentState(ctx, s, m); err != nil {
		t.Fatalf("restoreIncidentState: %v", err)
	}
	if got := m.Current("anything"); got != protocol.StateUnknown {
		t.Fatalf("Current(anything) = %v, want unknown", got)
	}
}

func TestRestoreIncidentStateRestoresMultipleTargetsIndependently(t *testing.T) {
	ctx := context.Background()
	s := openMainTestStore(t)
	now := time.Now()

	if _, err := s.OpenIncident(ctx, protocol.Incident{TargetID: "web-1", TargetName: "a", State: protocol.StateDown, StartedAt: now}); err != nil {
		t.Fatalf("OpenIncident web-1: %v", err)
	}
	if _, err := s.OpenIncident(ctx, protocol.Incident{TargetID: "web-2", TargetName: "b", State: protocol.StateWarning, StartedAt: now}); err != nil {
		t.Fatalf("OpenIncident web-2: %v", err)
	}

	m := incident.NewMachine(clock.Real())
	if err := restoreIncidentState(ctx, s, m); err != nil {
		t.Fatalf("restoreIncidentState: %v", err)
	}

	if got := m.Current("web-1"); got != protocol.StateDown {
		t.Fatalf("Current(web-1) = %v, want down", got)
	}
	if got := m.Current("web-2"); got != protocol.StateWarning {
		t.Fatalf("Current(web-2) = %v, want warning", got)
	}
}

// hubInfo must report the injected clock's time and the package version,
// not wall-clock time, so it is deterministic under a fake clock.
func TestHubInfoUsesInjectedClockAndVersion(t *testing.T) {
	fixed := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	c := clock.Fake(fixed)

	got := hubInfo(c)
	if !got.StartedAt.Equal(fixed) {
		t.Fatalf("StartedAt = %v, want %v", got.StartedAt, fixed)
	}
	if got.Version != version {
		t.Fatalf("Version = %q, want %q", got.Version, version)
	}
	if got.Host == "" {
		t.Fatal("Host is empty: want gopsutil's hostname or the os.Hostname fallback")
	}
}
