package cliclient

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

func fixedNow() time.Time { return time.Date(2026, 8, 24, 17, 50, 0, 0, time.UTC) }

func sampleSnapshot() protocol.Snapshot {
	now := fixedNow()
	return protocol.Snapshot{
		GeneratedAt: now.Add(-4 * time.Second),
		Overall:     protocol.StateDown,
		Counts:      protocol.Counts{Critical: 1, Healthy: 1},
		Hub:         protocol.HubInfo{Version: "0.1.0", Host: "mac", OS: "darwin", Kernel: "25.5.0"},
		Targets: []protocol.TargetStatus{
			{
				Target:    protocol.Target{ID: "host-local", Kind: protocol.KindHost, Name: "This Mac", IntervalSeconds: 15, Enabled: true},
				State:     protocol.StateHealthy,
				LastCheck: now,
				Metrics: map[string]float64{
					protocol.MetricCPUPercent:    54.8,
					protocol.MetricMemPercent:    77.5,
					protocol.MetricDiskPercent:   82.3,
					protocol.MetricUptimeSeconds: 90000,
				},
			},
			{
				Target:    protocol.Target{ID: "web-1", Kind: protocol.KindWebsite, Name: "Portfolio", Address: "https://levimackay.com", IntervalSeconds: 60, Enabled: true},
				State:     protocol.StateDown,
				LastCheck: now,
				Error:     "connection timeout",
			},
		},
		OpenIncidents: []protocol.Incident{{
			ID: 104, TargetID: "web-1", TargetName: "Portfolio", State: protocol.StateDown,
			StartedAt: now.Add(-3 * time.Minute), Summary: "connection timeout",
		}},
	}
}

func TestStatusRendersTheThingsThatMatter(t *testing.T) {
	var buf bytes.Buffer
	Renderer{Now: fixedNow()}.Status(&buf, sampleSnapshot())
	out := buf.String()

	for _, want := range []string{
		"Beacon", "problem detected",
		"Devices", "This Mac", "cpu 55%", "mem 78%", "disk 82%",
		"Websites", "Portfolio", "connection timeout",
		"Open incidents", "#104", "3m 0s",
		"1 critical, 1 healthy", "updated 4s ago",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusWithoutColorEmitsNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	Renderer{Now: fixedNow(), Color: false}.Status(&buf, sampleSnapshot())
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatal("ANSI escapes leaked into non-terminal output")
	}

	var colored bytes.Buffer
	Renderer{Now: fixedNow(), Color: true}.Status(&colored, sampleSnapshot())
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatal("color mode produced no escapes")
	}
}

func TestEmptySnapshotTellsYouWhatToDo(t *testing.T) {
	var buf bytes.Buffer
	Renderer{Now: fixedNow()}.Status(&buf, protocol.Snapshot{Overall: protocol.StateUnknown, GeneratedAt: fixedNow()})
	if !strings.Contains(buf.String(), "beacon add") {
		t.Fatalf("empty state should suggest the next action:\n%s", buf.String())
	}
}

func TestIncidentsRenderOngoingDistinctly(t *testing.T) {
	now := fixedNow()
	resolved := now.Add(-time.Hour)
	var buf bytes.Buffer
	Renderer{Now: now}.Incidents(&buf, []protocol.Incident{
		{ID: 104, TargetName: "Portfolio", State: protocol.StateDown, StartedAt: now.Add(-3 * time.Minute), Summary: "timeout"},
		{ID: 103, TargetName: "API", State: protocol.StateDown, StartedAt: now.Add(-time.Hour - 4*time.Minute - 13*time.Second), ResolvedAt: &resolved, Summary: "status 500"},
	})
	out := buf.String()
	if !strings.Contains(out, "ongoing") {
		t.Errorf("open incident not marked ongoing:\n%s", out)
	}
	if !strings.Contains(out, "4m 13s") {
		t.Errorf("resolved incident duration wrong:\n%s", out)
	}
}

func TestNoIncidentsIsPhrasedAsGoodNews(t *testing.T) {
	var buf bytes.Buffer
	Renderer{Now: fixedNow()}.Incidents(&buf, nil)
	if !strings.Contains(buf.String(), "No incidents") {
		t.Fatalf("unexpected empty rendering:\n%s", buf.String())
	}
}

func TestShortDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                              "0s",
		45 * time.Second:               "45s",
		4*time.Minute + 13*time.Second: "4m 13s",
		2*time.Hour + 30*time.Minute:   "2h 30m",
		50*time.Hour + 15*time.Minute:  "2d 2h",
		-5 * time.Second:               "0s",
	}
	for in, want := range cases {
		if got := shortDuration(in); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{512: "512B", 2048: "2.0KB", 5 * 1024 * 1024: "5.0MB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
