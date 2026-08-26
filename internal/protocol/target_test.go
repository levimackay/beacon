package protocol

import (
	"strings"
	"testing"
)

func TestTargetValidate(t *testing.T) {
	ok := Target{ID: "web-1", Kind: KindWebsite, Name: "Portfolio", Address: "https://example.com", IntervalSeconds: 60}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	host := Target{ID: "host-1", Kind: KindHost, Name: "This Mac", IntervalSeconds: 15}
	if err := host.Validate(); err != nil {
		t.Fatalf("host target needs no address, got: %v", err)
	}

	atCapID := Target{ID: strings.Repeat("a", maxIDLength), Kind: KindHost, Name: "x", IntervalSeconds: 60}
	if err := atCapID.Validate(); err != nil {
		t.Fatalf("id exactly at the length cap rejected: %v", err)
	}
	atCapName := Target{ID: "a", Kind: KindHost, Name: strings.Repeat("n", maxNameLength), IntervalSeconds: 60}
	if err := atCapName.Validate(); err != nil {
		t.Fatalf("name exactly at the length cap rejected: %v", err)
	}
	atCapContains := Target{ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60, Contains: strings.Repeat("c", maxContainsLength)}
	if err := atCapContains.Validate(); err != nil {
		t.Fatalf("contains exactly at the length cap rejected: %v", err)
	}

	bad := map[string]Target{
		"missing id":          {Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60},
		"missing name":        {ID: "a", Kind: KindWebsite, Address: "https://e.com", IntervalSeconds: 60},
		"unknown kind":        {ID: "a", Kind: "database", Name: "x", Address: "y", IntervalSeconds: 60},
		"missing address":     {ID: "a", Kind: KindWebsite, Name: "x", IntervalSeconds: 60},
		"interval too low":    {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 1},
		"interval negative":   {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: -60},
		"interval too high":   {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 90000},
		"bad status":          {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60, ExpectStatus: 42},
		"negative warn-after": {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60, WarnAfterMS: -1},
		"id over cap":         {ID: strings.Repeat("a", maxIDLength+1), Kind: KindHost, Name: "x", IntervalSeconds: 60},
		"name over cap":       {ID: "a", Kind: KindHost, Name: strings.Repeat("n", maxNameLength+1), IntervalSeconds: 60},
		"contains over cap":   {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60, Contains: strings.Repeat("c", maxContainsLength+1)},
	}
	for name, tgt := range bad {
		t.Run(name, func(t *testing.T) {
			if err := tgt.Validate(); err == nil {
				t.Fatalf("invalid target accepted: %+v", tgt)
			}
		})
	}
}
