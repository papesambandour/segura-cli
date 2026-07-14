package webserver

import (
	"encoding/json"
	"log"
	"net/http"

	"segura-cli/internal/webproxy"
)

// handleCredentials returns the list of available credentials as JSON,
// cache-first. Pass ?refresh=1 to force a live re-fetch (used by the Refresh
// button).
func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	var credentials []webproxy.Credential
	var err error
	if r.URL.Query().Get("refresh") != "" {
		credentials, err = s.sm.RefreshCredentials()
	} else {
		credentials, err = s.sm.GetCredentials()
	}
	if err != nil {
		log.Printf("Credentials error: %v", err)
		http.Error(w, `{"error":"failed to fetch credentials"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(credentials)
}

// handleStatus returns the server status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"host":   s.cfg.Host,
	})
}
