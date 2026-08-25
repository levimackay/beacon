package api

import "net/http"

// handleHealth is a liveness check only. It is the one unauthenticated
// route, so it must never disclose anything beyond "the process is up":
// no hostname, no target names, no counts.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.deps.Hub.Version,
	})
}
