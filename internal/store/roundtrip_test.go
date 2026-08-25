package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// fullyPopulatedTarget sets every field to a distinctive non-zero value.
// The reflection check below fails if a new field is added to
// protocol.Target and not set here, which is what forces this test to keep
// covering the whole struct rather than the fields that happened to exist
// when it was written.
func fullyPopulatedTarget() protocol.Target {
	return protocol.Target{
		ID:              "web-roundtrip",
		Kind:            protocol.KindWebsite,
		Name:            "Round Trip",
		Address:         "https://example.com/health",
		IntervalSeconds: 45,
		ExpectStatus:    204,
		Enabled:         true,
		AllowPrivate:    true,
	}
}

func TestTargetFixtureCoversEveryField(t *testing.T) {
	v := reflect.ValueOf(fullyPopulatedTarget())
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Errorf("protocol.Target.%s is zero in the round-trip fixture; "+
				"set it so the store is actually tested against that field",
				v.Type().Field(i).Name)
		}
	}
}

// Every field of a Target must survive a write and a read. A column that
// was never added to the schema drops its value silently: the write
// succeeds, the read returns the zero value, and for a field like
// AllowPrivate that means a permission the user granted quietly stops
// applying. Comparing the whole struct catches that for every field at
// once, including fields added later.
func TestTargetSurvivesAWriteAndRead(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "beacon.db"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	want := fullyPopulatedTarget()
	if err := s.UpsertTarget(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d targets, want 1", len(got))
	}
	if got[0] != want {
		t.Fatalf("target changed across a write and read:\n got %+v\nwant %+v", got[0], want)
	}
}

// An update must carry every field too, not just the ones an early version
// of the upsert happened to list.
func TestTargetUpdateCarriesEveryField(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "beacon.db"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	original := fullyPopulatedTarget()
	original.AllowPrivate = false
	original.ExpectStatus = 200
	if err := s.UpsertTarget(ctx, original); err != nil {
		t.Fatal(err)
	}

	updated := fullyPopulatedTarget()
	if err := s.UpsertTarget(ctx, updated); err != nil {
		t.Fatal(err)
	}

	got, err := s.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != updated {
		t.Fatalf("update lost a field:\n got %+v\nwant %+v", got[0], updated)
	}
}

// A database created by an older build has no allow_private column.
// Opening it must migrate rather than fail or silently keep dropping the
// value, because a real installation upgrades in place.
func TestOpenMigratesADatabaseFromAnOlderBuild(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "beacon.db")

	// Build the pre-migration targets table by hand.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `
		CREATE TABLE targets (
			id               TEXT PRIMARY KEY,
			kind             TEXT NOT NULL,
			name             TEXT NOT NULL,
			address          TEXT NOT NULL,
			interval_seconds INTEGER NOT NULL,
			expect_status    INTEGER NOT NULL DEFAULT 0,
			enabled          INTEGER NOT NULL DEFAULT 1
		);
		INSERT INTO targets (id, kind, name, address, interval_seconds, expect_status, enabled)
		VALUES ('legacy', 'website', 'Legacy', 'https://example.com', 60, 200, 1);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, clock.Real())
	if err != nil {
		t.Fatalf("opening a database from an older build: %v", err)
	}
	defer s.Close()

	got, err := s.Targets(ctx)
	if err != nil {
		t.Fatalf("reading migrated targets: %v", err)
	}
	if len(got) != 1 || got[0].ID != "legacy" {
		t.Fatalf("existing rows did not survive the migration: %+v", got)
	}
	if got[0].AllowPrivate {
		t.Error("a target from before the column existed should default to no private access")
	}

	// And the new column is now usable.
	updated := got[0]
	updated.AllowPrivate = true
	if err := s.UpsertTarget(ctx, updated); err != nil {
		t.Fatal(err)
	}
	after, err := s.Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !after[0].AllowPrivate {
		t.Fatal("the migrated column does not persist a value")
	}
}

// Opening the same database twice must not re-run a migration in a way
// that errors, since every start of the hub calls Open.
func TestMigrationsAreIdempotentAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beacon.db")
	for i := range 3 {
		s, err := Open(path, clock.Real())
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := s.UpsertTarget(context.Background(), fullyPopulatedTarget()); err != nil {
			t.Fatalf("upsert on open %d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
}
