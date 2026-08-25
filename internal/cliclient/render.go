package cliclient

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

// ANSI attributes, used only when the destination is a terminal.
const (
	reset  = "\x1b[0m"
	dim    = "\x1b[2m"
	bold   = "\x1b[1m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[31m"
)

// Renderer writes human-readable Beacon output.
type Renderer struct {
	Color bool
	Now   time.Time
}

func (r Renderer) paint(s, attr string) string {
	if !r.Color {
		return s
	}
	return attr + s + reset
}

func (r Renderer) attrFor(s protocol.State) string {
	switch s {
	case protocol.StateHealthy:
		return green
	case protocol.StateWarning:
		return yellow
	case protocol.StateDown:
		return red
	default:
		return dim
	}
}

// glyph is the state marker. It stays a single cell wide in every state so
// that columns line up whatever the health is.
func (r Renderer) glyph(s protocol.State) string {
	return r.paint("●", r.attrFor(s))
}

// Status renders the top-level answer to "is everything okay".
func (r Renderer) Status(w io.Writer, s protocol.Snapshot) {
	headline := map[protocol.State]string{
		protocol.StateHealthy: "everything is healthy",
		protocol.StateWarning: "needs attention",
		protocol.StateDown:    "problem detected",
		protocol.StateUnknown: "status unknown",
	}[s.Overall]

	fmt.Fprintf(w, "\n  %s  %s\n\n", r.paint("Beacon", bold), r.paint(headline, r.attrFor(s.Overall)))

	if len(s.Targets) == 0 {
		fmt.Fprintf(w, "  %s\n\n", r.paint("Nothing is being monitored yet. Try: beacon add https://example.com --name Site", dim))
		return
	}

	byKind := map[protocol.TargetKind][]protocol.TargetStatus{}
	for _, t := range s.Targets {
		byKind[t.Target.Kind] = append(byKind[t.Target.Kind], t)
	}
	order := []struct {
		kind  protocol.TargetKind
		label string
	}{
		{protocol.KindHost, "Devices"},
		{protocol.KindWebsite, "Websites"},
		{protocol.KindService, "Services"},
	}

	for _, group := range order {
		items := byKind[group.kind]
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Target.Name < items[j].Target.Name })
		fmt.Fprintf(w, "  %s\n", r.paint(group.label, dim))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, it := range items {
			fmt.Fprintf(tw, "  %s %s\t%s\n", r.glyph(it.State), it.Target.Name, r.detail(it))
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	if len(s.OpenIncidents) > 0 {
		fmt.Fprintf(w, "  %s\n", r.paint("Open incidents", dim))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, in := range s.OpenIncidents {
			fmt.Fprintf(tw, "  %s #%d %s\tfor %s\t%s\n",
				r.glyph(in.State), in.ID, in.TargetName,
				shortDuration(in.Duration(r.Now)), in.Summary)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "  %s\n\n", r.paint(summaryLine(s)+" · updated "+ago(r.Now, s.GeneratedAt), dim))
}

// detail is the per-target right-hand column: the numbers that matter for
// that kind of target, and nothing else.
func (r Renderer) detail(t protocol.TargetStatus) string {
	if t.Error != "" {
		return r.paint(t.Error, r.attrFor(t.State))
	}
	var parts []string
	switch t.Target.Kind {
	case protocol.KindHost:
		for _, m := range []struct{ key, label string }{
			{protocol.MetricCPUPercent, "cpu"},
			{protocol.MetricMemPercent, "mem"},
			{protocol.MetricDiskPercent, "disk"},
		} {
			if v, ok := t.Metrics[m.key]; ok {
				parts = append(parts, fmt.Sprintf("%s %.0f%%", m.label, v))
			}
		}
		if v, ok := t.Metrics[protocol.MetricTempC]; ok {
			parts = append(parts, fmt.Sprintf("%.0f°C", v))
		}
		if v, ok := t.Metrics[protocol.MetricUptimeSeconds]; ok {
			parts = append(parts, "up "+shortDuration(time.Duration(v)*time.Second))
		}
	default:
		if t.LatencyMS > 0 {
			parts = append(parts, fmt.Sprintf("%.0fms", t.LatencyMS))
		}
		if v, ok := t.Metrics[protocol.MetricCertDaysLeft]; ok {
			parts = append(parts, fmt.Sprintf("cert %.0fd", v))
		}
	}
	if len(parts) == 0 {
		return r.paint("no data yet", dim)
	}
	return strings.Join(parts, "  ")
}

func summaryLine(s protocol.Snapshot) string {
	var parts []string
	add := func(n int, word string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, word))
		}
	}
	add(s.Counts.Critical, "critical")
	add(s.Counts.Warning, "warning")
	add(s.Counts.Healthy, "healthy")
	add(s.Counts.Unknown, "unknown")
	if len(parts) == 0 {
		return "nothing monitored"
	}
	return strings.Join(parts, ", ")
}

// Targets renders the configured target list.
func (r Renderer) Targets(w io.Writer, ts []protocol.Target) {
	if len(ts) == 0 {
		fmt.Fprintf(w, "\n  %s\n\n", r.paint("No targets configured.", dim))
		return
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n",
		r.paint("ID", dim), r.paint("NAME", dim), r.paint("KIND", dim), r.paint("EVERY", dim))
	for _, t := range ts {
		name := t.Name
		if !t.Enabled {
			name += r.paint(" (paused)", dim)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", t.ID, name, t.Kind, shortDuration(t.Interval()))
	}
	tw.Flush()
	fmt.Fprintln(w)
}

// Incidents renders incident history, newest first.
func (r Renderer) Incidents(w io.Writer, in []protocol.Incident) {
	if len(in) == 0 {
		fmt.Fprintf(w, "\n  %s\n\n", r.paint("No incidents in this window. That is the good outcome.", dim))
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, i := range in {
		when := i.StartedAt.Local().Format("Jan 2 15:04")
		dur := shortDuration(i.Duration(r.Now))
		if i.Open() {
			dur = r.paint("ongoing "+dur, r.attrFor(i.State))
		}
		fmt.Fprintf(tw, "  %s #%d\t%s\t%s\t%s\t%s\n",
			r.glyph(i.State), i.ID, i.TargetName, when, dur, i.Summary)
	}
	tw.Flush()
	fmt.Fprintln(w)
}

// Diagnostics renders the troubleshooting view.
func (r Renderer) Diagnostics(w io.Writer, d protocol.Diagnostics) {
	ok := func(b bool) string {
		if b {
			return r.paint("✓", green)
		}
		return r.paint("✗", red)
	}
	fmt.Fprintf(w, "\n  %s\n\n", r.paint("Beacon Diagnostics", bold))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  Hub\t%s\t%s\n", ok(true), d.Hub.Version)
	fmt.Fprintf(tw, "  Scheduler\t%s\tlast tick %s\n", ok(d.SchedulerHealthy), ago(r.Now, d.LastTick))
	fmt.Fprintf(tw, "  Database\t%s\t%s, %d raw / %d 5m / %d 1h samples\n",
		ok(d.Store.SizeBytes > 0), humanBytes(d.Store.SizeBytes),
		d.Store.RawSamples, d.Store.Bucket5m, d.Store.Bucket1h)
	fmt.Fprintf(tw, "  Tailscale\t%s\t%s\n", ok(d.TailscaleState == "running"), d.TailscaleState)
	fmt.Fprintf(tw, "  API latency\t%s\t%.0fms\n", ok(d.APILatencyMS < 250), d.APILatencyMS)
	fmt.Fprintf(tw, "  Host\t\t%s %s (%s)\n", d.Hub.OS, d.Hub.Kernel, d.Hub.Host)
	fmt.Fprintf(tw, "  Hub uptime\t\t%s\n", shortDuration(time.Duration(d.Hub.UptimeSeconds)*time.Second))
	tw.Flush()
	fmt.Fprintln(w)
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func ago(now, then time.Time) string {
	if then.IsZero() {
		return "never"
	}
	return shortDuration(now.Sub(then)) + " ago"
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGT"[exp])
}
