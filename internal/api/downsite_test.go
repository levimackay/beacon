package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/levimackay/beacon/internal/protocol"
)

// A monitoring tool must accept a target that is currently unreachable.
// Refusing to add a site because that site is down is backwards: being down
// is precisely the condition the user is asking to be told about.
func TestAddingAnUnresolvableSiteSucceeds(t *testing.T) {
	h, st := newTestServer(t)

	body, _ := json.Marshal(protocol.Target{
		Kind:            protocol.KindWebsite,
		Name:            "Broken Site",
		Address:         "https://this-host-does-not-resolve-beacon-test.invalid",
		IntervalSeconds: 60,
		Enabled:         true,
	})
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200: an unreachable site must still be addable",
			rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := len(st.targets); got != 1 {
		t.Fatalf("stored %d targets, want the target persisted", got)
	}
}

// An address that resolves into a forbidden range is still refused, and
// still never reaches the store. The unresolvable case must not have opened
// a hole in the range check.
func TestForbiddenRangeIsStillRefusedAfterTheUnresolvableAllowance(t *testing.T) {
	for _, addr := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:9999/",
		"http://10.0.0.1/",
	} {
		t.Run(addr, func(t *testing.T) {
			h, st := newTestServer(t)
			body, _ := json.Marshal(protocol.Target{
				Kind:            protocol.KindWebsite,
				Name:            "Bad",
				Address:         addr,
				IntervalSeconds: 60,
				Enabled:         true,
			})
			rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %s", rec.Code, addr)
			}
			if len(st.targets) != 0 {
				t.Fatalf("a refused target reached the store: %+v", st.targets)
			}
		})
	}
}
