package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/levimackay/beacon/internal/protocol"
)

func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	targets, err := s.deps.Store.Targets(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read targets")
		return
	}
	latest, err := s.deps.Store.LatestSamples(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read samples")
		return
	}
	openIncidents, err := s.deps.Store.OpenIncidents(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read incidents")
		return
	}
	if openIncidents == nil {
		openIncidents = []protocol.Incident{}
	}

	statuses := make([]protocol.TargetStatus, 0, len(targets))
	for _, t := range targets {
		ts := protocol.TargetStatus{Target: t, State: protocol.StateUnknown}
		if sample, ok := latest[t.ID]; ok {
			ts.State = sample.State
			ts.LatencyMS = sample.LatencyMS
			ts.Metrics = sample.Metrics
			ts.LastCheck = sample.At
			ts.Error = sample.Error
			ts.CertExpiry = sample.CertExpiry
		}
		statuses = append(statuses, ts)
	}

	snap := protocol.Snapshot{
		GeneratedAt:   s.deps.Clock.Now(),
		Overall:       protocol.Overall(statuses),
		Hub:           s.hubInfo(),
		Counts:        protocol.Tally(statuses),
		Targets:       statuses,
		OpenIncidents: openIncidents,
	}

	body, err := json.Marshal(snap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode snapshot")
		return
	}

	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
