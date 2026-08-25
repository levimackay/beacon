package protocol

import "testing"

func TestTargetValidate(t *testing.T) {
	ok := Target{ID: "web-1", Kind: KindWebsite, Name: "Portfolio", Address: "https://example.com", IntervalSeconds: 60}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	host := Target{ID: "host-1", Kind: KindHost, Name: "This Mac", IntervalSeconds: 15}
	if err := host.Validate(); err != nil {
		t.Fatalf("host target needs no address, got: %v", err)
	}

	bad := map[string]Target{
		"missing id":        {Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60},
		"missing name":      {ID: "a", Kind: KindWebsite, Address: "https://e.com", IntervalSeconds: 60},
		"unknown kind":      {ID: "a", Kind: "database", Name: "x", Address: "y", IntervalSeconds: 60},
		"missing address":   {ID: "a", Kind: KindWebsite, Name: "x", IntervalSeconds: 60},
		"interval too low":  {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 1},
		"interval negative": {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: -60},
		"interval too high": {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 90000},
		"bad status":        {ID: "a", Kind: KindWebsite, Name: "x", Address: "https://e.com", IntervalSeconds: 60, ExpectStatus: 42},
	}
	for name, tgt := range bad {
		t.Run(name, func(t *testing.T) {
			if err := tgt.Validate(); err == nil {
				t.Fatalf("invalid target accepted: %+v", tgt)
			}
		})
	}
}
