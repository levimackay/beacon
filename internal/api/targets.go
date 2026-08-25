package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/protocol"
)

// maxTargetBodyBytes bounds a POST /v1/targets body well above any sane
// target definition, so a client can't tie up the handler decoding a
// multi-megabyte payload.
const maxTargetBodyBytes = 64 * 1024

func (s *server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.deps.Store.Targets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read targets")
		return
	}
	if targets == nil {
		targets = []protocol.Target{}
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *server) handlePostTarget(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTargetBodyBytes)

	var t protocol.Target
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, decodeJSONError(err))
		return
	}

	if t.ID == "" {
		id, err := randomID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate target id")
			return
		}
		t.ID = id
	}
	setAuditTarget(r, t.ID)

	if err := t.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// SSRF check runs before any store write, and only for website targets
	// (the only kind that dials a client-supplied address). Guard is copied
	// per request because AllowPrivate is a per-target opt-in, not a
	// process-wide setting.
	if t.Kind == protocol.KindWebsite {
		g := *s.deps.Guard
		g.AllowPrivate = t.AllowPrivate
		// A host that cannot be resolved right now is accepted: a site
		// being unreachable is the condition the user is asking Beacon
		// to watch for, so refusing to add it would be backwards. It is
		// recorded and reported as down. Every probe still passes
		// through the guard at dial time, so accepting the target
		// grants it no reach it would not otherwise have.
		if err := g.CheckURL(t.Address); err != nil && !errors.Is(err, collect.ErrUnresolvable) {
			writeError(w, http.StatusBadRequest, "address rejected: "+err.Error())
			return
		}
	}

	if err := s.deps.Store.UpsertTarget(r.Context(), t); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save target")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	setAuditTarget(r, id)

	targets, err := s.deps.Store.Targets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read targets")
		return
	}
	found := false
	for _, t := range targets {
		if t.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "target not found")
		return
	}

	if err := s.deps.Store.DeleteTarget(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete target")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// randomID returns a random hex id, not a monotonic counter, so ids don't
// leak how many targets have ever been created.
func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
