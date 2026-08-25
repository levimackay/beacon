package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// Deleting a target must close any incident still open against it. Nothing
// will ever check that target again, so an incident left open can never
// recover: it would sit in the open list forever and keep Beacon reporting
// a problem for something the user deliberately removed.
func TestDeletingATargetResolvesItsOpenIncident(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC)
	c := clock.Fake(now)

	s, err := Open(filepath.Join(t.TempDir(), "beacon.db"), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tgt := protocol.Target{
		ID: "web-1", Kind: protocol.KindWebsite, Name: "Local Service",
		Address: "https://example.com", IntervalSeconds: 60, Enabled: true,
	}
	if err := s.UpsertTarget(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenIncident(ctx, protocol.Incident{
		TargetID: tgt.ID, TargetName: tgt.Name, State: protocol.StateDown,
		StartedAt: now, Summary: "connection refused",
	}); err != nil {
		t.Fatal(err)
	}

	open, err := s.OpenIncidents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("expected one open incident before deletion, got %d", len(open))
	}

	c.Advance(5 * time.Minute)
	if err := s.DeleteTarget(ctx, tgt.ID); err != nil {
		t.Fatal(err)
	}

	open, err = s.OpenIncidents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("deleting a target left %d incident(s) open forever: %+v", len(open), open)
	}

	// The outage still happened, so the history is kept.
	all, err := s.Incidents(ctx, IncidentFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("incident history was discarded: %+v", all)
	}
	if all[0].ResolvedAt == nil {
		t.Fatal("the retained incident is still marked open")
	}
	if got := all[0].Duration(c.Now()); got != 5*time.Minute {
		t.Fatalf("incident duration = %v, want it closed at deletion time", got)
	}
}

// Deleting a target with nothing open must not fail or invent an incident.
func TestDeletingAHealthyTargetIsClean(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "beacon.db"), clock.Real())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tgt := protocol.Target{
		ID: "web-1", Kind: protocol.KindWebsite, Name: "Fine",
		Address: "https://example.com", IntervalSeconds: 60, Enabled: true,
	}
	if err := s.UpsertTarget(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTarget(ctx, tgt.ID); err != nil {
		t.Fatalf("deleting a healthy target: %v", err)
	}
	all, err := s.Incidents(ctx, IncidentFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("deletion invented an incident: %+v", all)
	}
}
