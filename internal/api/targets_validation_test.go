package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Interval and ExpectStatus are both plain numeric range checks in
// Target.Validate; only the negative-interval case had a test. This sweeps
// both boundaries exactly (one below, at, one above).
func TestPostTargetIntervalAndExpectStatusBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"interval zero rejected", `{"id":"t1","kind":"host","name":"n","intervalSeconds":0,"enabled":true}`, http.StatusBadRequest},
		{"interval one below minimum rejected", `{"id":"t1","kind":"host","name":"n","intervalSeconds":4,"enabled":true}`, http.StatusBadRequest},
		{"interval at minimum accepted", `{"id":"t1","kind":"host","name":"n","intervalSeconds":5,"enabled":true}`, http.StatusOK},
		{"interval at maximum accepted", `{"id":"t1","kind":"host","name":"n","intervalSeconds":86400,"enabled":true}`, http.StatusOK},
		{"interval one above maximum rejected", `{"id":"t1","kind":"host","name":"n","intervalSeconds":86401,"enabled":true}`, http.StatusBadRequest},
		{"expectStatus one below minimum rejected", `{"id":"t1","kind":"host","name":"n","intervalSeconds":30,"enabled":true,"expectStatus":99}`, http.StatusBadRequest},
		{"expectStatus at minimum accepted", `{"id":"t1","kind":"host","name":"n","intervalSeconds":30,"enabled":true,"expectStatus":100}`, http.StatusOK},
		{"expectStatus at maximum accepted", `{"id":"t1","kind":"host","name":"n","intervalSeconds":30,"enabled":true,"expectStatus":599}`, http.StatusOK},
		{"expectStatus one above maximum rejected", `{"id":"t1","kind":"host","name":"n","intervalSeconds":30,"enabled":true,"expectStatus":600}`, http.StatusBadRequest},
		{"expectStatus zero means unset, accepted", `{"id":"t1","kind":"host","name":"n","intervalSeconds":30,"enabled":true,"expectStatus":0}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestServer(t)
			rec := authedReq(t, h, http.MethodPost, "/v1/targets", []byte(tc.body))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestPostTargetEmptyNameIs400(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"host","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestPostTargetUnknownKindIs400(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"toaster","name":"n","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestPostTargetWebsiteWithoutAddressIs400(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"website","name":"n","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// A host target has no address to check: Validate only requires one for
// non-host kinds. No existing test exercises a host with no address at all.
func TestPostTargetHostWithoutAddressSucceeds(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"host","name":"n","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestPostTargetNegativeWarnAfterMSIs400(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"website","name":"n","address":"https://example.com","intervalSeconds":30,"enabled":true,"warnAfterMs":-1}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// A scheme other than http/https is rejected by the guard before any
// network activity, distinct from (and checked earlier than) the SSRF
// range check: this never resolves DNS, so it is deterministic.
func TestPostTargetRejectsDisallowedScheme(t *testing.T) {
	h, fs := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"website","name":"n","address":"ftp://example.com","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if fs.upserts() != 0 {
		t.Fatalf("upserts = %d, want 0: a disallowed-scheme target must never be persisted", fs.upserts())
	}
}

// A URL with no host at all (an empty authority) is rejected the same way,
// also without touching the network.
func TestPostTargetRejectsURLWithNoHost(t *testing.T) {
	h, _ := newTestServer(t)
	body := []byte(`{"id":"t1","kind":"website","name":"n","address":"http:///no-host","intervalSeconds":30,"enabled":true}`)
	rec := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// No POST body field, and no request-level validation, ever enforces a
// unique Name: only ID is a key (UpsertTarget upserts by id; the SQL
// schema's only uniqueness is on id). Two targets sharing a display name is
// accepted, current, and matches how nothing in design.md asks for name
// uniqueness. This pins that down as intentional rather than an untested
// gap: if a uniqueness rule is ever wanted, it belongs in Target.Validate,
// and this test would need to change alongside it.
func TestPostTargetAllowsDuplicateNames(t *testing.T) {
	h, fs := newTestServer(t)
	first := []byte(`{"id":"t1","kind":"host","name":"Prod DB","intervalSeconds":30,"enabled":true}`)
	second := []byte(`{"id":"t2","kind":"host","name":"Prod DB","intervalSeconds":30,"enabled":true}`)

	if rec := authedReq(t, h, http.MethodPost, "/v1/targets", first); rec.Code != http.StatusOK {
		t.Fatalf("first insert status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := authedReq(t, h, http.MethodPost, "/v1/targets", second); rec.Code != http.StatusOK {
		t.Fatalf("second insert (duplicate name, distinct id) status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fs.upserts() != 2 {
		t.Fatalf("upserts = %d, want 2 (both kept, keyed by distinct ids)", fs.upserts())
	}
}

// An omitted id is generated by the server, not rejected, and two omitted
// ids in a row must not collide (which would silently overwrite the first
// target instead of creating a second one).
func TestPostTargetWithoutIDGeneratesOne(t *testing.T) {
	h, fs := newTestServer(t)
	body := []byte(`{"kind":"host","name":"n","intervalSeconds":30,"enabled":true}`)

	rec1 := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec1.Code, rec1.Body.String())
	}
	var got1 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec1.Body.Bytes(), &got1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got1.ID == "" {
		t.Fatal("generated id is empty")
	}

	rec2 := authedReq(t, h, http.MethodPost, "/v1/targets", body)
	var got2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got2.ID == "" || got2.ID == got1.ID {
		t.Fatalf("second generated id = %q, first = %q, want a distinct non-empty id", got2.ID, got1.ID)
	}
	if fs.upserts() != 2 {
		t.Fatalf("upserts = %d, want 2 (generated ids must not collide)", fs.upserts())
	}
}
