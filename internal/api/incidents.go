package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/store"
)

func (s *server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.IncidentFilter{TargetID: q.Get("target")}

	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since: must be RFC3339")
			return
		}
		f.Since = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until: must be RFC3339")
			return
		}
		f.Until = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit: must be a non-negative integer")
			return
		}
		f.Limit = n
	}

	incidents, err := s.deps.Store.Incidents(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read incidents")
		return
	}
	if incidents == nil {
		incidents = []protocol.Incident{}
	}
	writeJSON(w, http.StatusOK, incidents)
}
