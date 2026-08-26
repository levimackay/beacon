package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

// GET /v1/incidents had no coverage at all beyond the auth matrix (which
// only proves it requires a token, not that its filtering works).

func TestListIncidentsEmptyReturnsEmptyArrayNotNull(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodGet, "/v1/incidents", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []protocol.Incident
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got == nil {
		t.Fatal("decoded slice is nil: a bare JSON null instead of [] will break a client expecting an array")
	}
	if len(got) != 0 {
		t.Fatalf("incidents = %+v, want none", got)
	}
}

func TestListIncidentsFiltersByTarget(t *testing.T) {
	h, fs := newTestServer(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := fs.OpenIncident(ctx, protocol.Incident{TargetID: "web-1", TargetName: "Site 1", State: protocol.StateDown, StartedAt: at}); err != nil {
		t.Fatalf("seed web-1: %v", err)
	}
	if _, err := fs.OpenIncident(ctx, protocol.Incident{TargetID: "web-2", TargetName: "Site 2", State: protocol.StateDown, StartedAt: at}); err != nil {
		t.Fatalf("seed web-2: %v", err)
	}

	rec := authedReq(t, h, http.MethodGet, "/v1/incidents?target=web-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got []protocol.Incident
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].TargetID != "web-1" {
		t.Fatalf("incidents = %+v, want exactly the one against web-1", got)
	}
}

func TestListIncidentsInvalidSinceIs400(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodGet, "/v1/incidents?since=not-a-date", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestListIncidentsInvalidUntilIs400(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodGet, "/v1/incidents?until=not-a-date", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestListIncidentsMalformedLimitIs400(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodGet, "/v1/incidents?limit=abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestListIncidentsNegativeLimitIs400(t *testing.T) {
	h, _ := newTestServer(t)
	rec := authedReq(t, h, http.MethodGet, "/v1/incidents?limit=-1", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// limit=0 is accepted, not rejected: the handler only rejects a negative
// count. Zero passes straight through as IncidentFilter.Limit, which the
// store treats as "no limit" rather than "return nothing" (see the
// `filter.Limit > 0` guard in fakeStore.Incidents and its real counterpart).
func TestListIncidentsZeroLimitIsAcceptedAndMeansNoLimit(t *testing.T) {
	h, fs := newTestServer(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := fs.OpenIncident(ctx, protocol.Incident{TargetID: "web-1", TargetName: "Site 1", State: protocol.StateDown, StartedAt: at}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rec := authedReq(t, h, http.MethodGet, "/v1/incidents?limit=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []protocol.Incident
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("incidents = %+v, want all 3 (limit=0 must not truncate to nothing)", got)
	}
}

// since/until are boundary-inclusive: the filter excludes only StartedAt
// strictly before Since or strictly after Until.
func TestListIncidentsSinceUntilRangeIsInclusive(t *testing.T) {
	h, fs := newTestServer(t)
	ctx := context.Background()
	day1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{day1, day2, day3} {
		if _, err := fs.OpenIncident(ctx, protocol.Incident{TargetID: "web-1", TargetName: "Site 1", State: protocol.StateDown, StartedAt: at}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	path := "/v1/incidents?since=" + day1.Format(time.RFC3339) + "&until=" + day2.Format(time.RFC3339)
	rec := authedReq(t, h, http.MethodGet, path, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got []protocol.Incident
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("incidents = %+v, want exactly 2 (day1 and day2 inclusive; day3 excluded)", got)
	}
	for _, in := range got {
		if in.StartedAt.After(day2) {
			t.Fatalf("incident %+v started after the until bound", in)
		}
	}
}
