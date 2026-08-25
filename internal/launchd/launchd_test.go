package launchd

import (
	"encoding/xml"
	"strings"
	"testing"
)

// The plist must parse as XML. A malformed one is rejected by launchd with a
// message that gives the user nothing to act on, so catch it here instead.
func TestPlistIsWellFormedXML(t *testing.T) {
	data, err := Plist(Config{
		BinaryPath: "/usr/local/bin/beaconhub",
		SupportDir: "/Users/someone/Library/Application Support/Beacon",
	})
	if err != nil {
		t.Fatalf("Plist: %v", err)
	}
	var v any
	if err := xml.Unmarshal(data, &v); err != nil {
		t.Fatalf("plist is not well-formed XML: %v\n%s", err, data)
	}
	for _, want := range []string{Label, "/usr/local/bin/beaconhub", "RunAtLoad", "ThrottleInterval"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plist missing %q:\n%s", want, data)
		}
	}
}

// A home directory can contain characters that are markup in XML. Beacon does
// not choose the user's account name, so the path has to be escaped.
func TestPlistEscapesPathsContainingMarkup(t *testing.T) {
	data, err := Plist(Config{
		BinaryPath: "/Users/a&b/bin/beaconhub",
		SupportDir: "/Users/a&b/Library/Application Support/Beacon",
	})
	if err != nil {
		t.Fatalf("Plist: %v", err)
	}
	if strings.Contains(string(data), "a&b") {
		t.Fatalf("ampersand was not escaped:\n%s", data)
	}
	if !strings.Contains(string(data), "a&amp;b") {
		t.Fatalf("expected an escaped ampersand:\n%s", data)
	}
	var v any
	if err := xml.Unmarshal(data, &v); err != nil {
		t.Fatalf("escaped plist is not well-formed: %v", err)
	}
}

// launchd needs absolute paths. A relative one produces an agent that fails
// to start with no clear reason, so refuse it at the point of construction.
func TestPlistRequiresAbsolutePaths(t *testing.T) {
	if _, err := Plist(Config{BinaryPath: "beaconhub", SupportDir: "/tmp"}); err == nil {
		t.Error("relative binary path was accepted")
	}
	if _, err := Plist(Config{BinaryPath: "/usr/local/bin/beaconhub", SupportDir: "relative"}); err == nil {
		t.Error("relative support directory was accepted")
	}
}

// KeepAlive must be conditional on abnormal exit. An unconditional KeepAlive
// would restart the hub during an uninstall, which reads to the user as
// Beacon refusing to be removed.
func TestPlistRestartsOnlyOnAbnormalExit(t *testing.T) {
	data, err := Plist(Config{BinaryPath: "/usr/local/bin/beaconhub", SupportDir: "/tmp/beacon"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "<key>SuccessfulExit</key>") || !strings.Contains(s, "<false/>") {
		t.Fatalf("KeepAlive is not conditional on abnormal exit:\n%s", s)
	}
}

func TestPlistCarriesCustomPort(t *testing.T) {
	data, err := Plist(Config{BinaryPath: "/usr/local/bin/beaconhub", SupportDir: "/tmp/beacon", Port: 51000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "BEACON_PORT") || !strings.Contains(string(data), "51000") {
		t.Fatalf("custom port not carried into the agent environment:\n%s", data)
	}

	plain, err := Plist(Config{BinaryPath: "/usr/local/bin/beaconhub", SupportDir: "/tmp/beacon"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "BEACON_PORT") {
		t.Error("default port should not be pinned into the agent environment")
	}
}

func TestPlistPathIsUnderLaunchAgents(t *testing.T) {
	p, err := PlistPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "Library/LaunchAgents") || !strings.HasSuffix(p, Label+".plist") {
		t.Fatalf("PlistPath = %q", p)
	}
}

// The agent is per-user. A system domain would need root, which is the setup
// friction this design exists to avoid.
func TestDomainIsPerUserGUI(t *testing.T) {
	if d := domain(); !strings.HasPrefix(d, "gui/") {
		t.Fatalf("domain = %q, want a per-user gui domain", d)
	}
}
