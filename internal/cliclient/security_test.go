package cliclient

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

// TestSecurity_TargetNameEscapeSequencesAreStripped demonstrates
// that Target.Name — a string any local process holding the bearer token can
// set via POST /v1/targets, with no charset or length restriction in
// protocol.Target.Validate — is written straight into the terminal by
// Renderer.Status and Renderer.Targets with fmt.Fprintf and no stripping of
// control or ANSI escape bytes.
//
// A target named with an OSC 52 sequence (widely supported: iTerm2, kitty,
// Windows Terminal, many others) silently overwrites the system clipboard
// with attacker-chosen content the next time the legitimate user runs
// `beacon status` or `beacon targets`. The same unsanitized path also allows
// title-bar spoofing, hidden/invisible text and cursor-repositioning tricks
// to make fabricated output look like it came from Beacon. This crosses a
// real boundary even though attacker and victim run as the same OS user:
// the attacker only needs API access (any local process holding the token),
// not the ability to run arbitrary code in the terminal the victim is
// actually looking at.
func TestSecurity_TargetNameEscapeSequencesAreStripped(t *testing.T) {
	const osc52ClipboardWrite = "\x1b]52;c;cHduZWQ=\x07" // sets the clipboard to "pwned"

	snap := protocol.Snapshot{
		Overall: protocol.StateHealthy,
		Targets: []protocol.TargetStatus{{
			Target: protocol.Target{
				ID:      "web-1",
				Kind:    protocol.KindWebsite,
				Name:    "Portfolio" + osc52ClipboardWrite,
				Address: "https://example.com",
				Enabled: true,
			},
			State: protocol.StateHealthy,
		}},
	}

	var buf bytes.Buffer
	Renderer{Now: time.Now()}.Status(&buf, snap)

	if bytes.Contains(buf.Bytes(), []byte(osc52ClipboardWrite)) {
		t.Fatalf("Renderer.Status wrote a raw OSC 52 escape sequence to the terminal stream unchanged; "+
			"a target Name controlled by any local caller of POST /v1/targets can hijack the clipboard "+
			"or spoof output of the next `beacon status`.\noutput: %q", buf.String())
	}
}

// Colour codes this package adds itself must survive sanitizing, or the
// fix would silently strip Beacon's own output.
func TestSecurity_SanitizingKeepsBeaconsOwnColour(t *testing.T) {
	var buf bytes.Buffer
	Renderer{Now: fixedNow(), Color: true}.Status(&buf, sampleSnapshot())
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Fatal("sanitizing stripped the renderer's own colour codes")
	}
}

// A tab inside a value would shift every column on the row, letting a
// hostile name rewrite the shape of the table around it.
func TestSecurity_TabsInValuesCannotBreakColumnAlignment(t *testing.T) {
	snap := sampleSnapshot()
	snap.Targets[1].Target.Name = "Evil\tName"
	snap.Targets[1].Error = "broke\there"

	var buf bytes.Buffer
	Renderer{Now: fixedNow()}.Status(&buf, snap)

	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "Evil") && strings.Contains(line, "\t") {
			t.Fatalf("a tab from a target name reached the output: %q", line)
		}
	}
}

// Carriage returns let printed text be overwritten after the fact, so what
// the reader sees is not what Beacon wrote.
func TestSecurity_CarriageReturnsCannotOverwritePrintedOutput(t *testing.T) {
	snap := sampleSnapshot()
	snap.Targets[1].Target.Name = "Portfolio\r        ALL SYSTEMS NORMAL"

	var buf bytes.Buffer
	Renderer{Now: fixedNow()}.Status(&buf, snap)
	if strings.Contains(buf.String(), "\r") {
		t.Fatalf("a carriage return reached the terminal:\n%q", buf.String())
	}
}

// Incident summaries come partly from the monitored server's own error
// text, so they are attacker-influenced too.
func TestSecurity_IncidentSummariesAreSanitized(t *testing.T) {
	var buf bytes.Buffer
	Renderer{Now: fixedNow()}.Incidents(&buf, []protocol.Incident{{
		ID: 1, TargetName: "Site\x1b]0;pwned\a", State: protocol.StateDown,
		StartedAt: fixedNow().Add(-time.Minute),
		Summary:   "status 500\x1b]52;c;cHduZWQ=\a",
	}})
	if strings.Contains(buf.String(), "\x1b]") {
		t.Fatalf("an OSC sequence reached the terminal via an incident:\n%q", buf.String())
	}
}
