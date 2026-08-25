package api

import (
	"net/http"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

func (s *server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	stats, err := s.deps.Store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read store stats")
		return
	}

	var lastTick time.Time
	healthy := false
	if s.deps.Scheduler != nil {
		lastTick = s.deps.Scheduler.LastTick()
		age := s.deps.Clock.Now().Sub(lastTick)
		if age < 0 {
			age = -age
		}
		healthy = age <= schedulerHealthyWindow
	}

	writeJSON(w, http.StatusOK, protocol.Diagnostics{
		Hub: s.hubInfo(),
		Store: protocol.StoreStats{
			Targets:       stats.Targets,
			RawSamples:    stats.RawSamples,
			Bucket5m:      stats.Bucket5m,
			Bucket1h:      stats.Bucket1h,
			OpenIncidents: stats.OpenIncidents,
			SizeBytes:     stats.SizeBytes,
		},
		LastTick:         lastTick,
		SchedulerHealthy: healthy,
		TailscaleState:   "unavailable",
	})
}
