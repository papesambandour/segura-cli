package webserver

import (
	"encoding/json"
	"log"
	"net/http"
)

// handleCredentials returns the list of available credentials as JSON.
func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	client, err := s.sm.GetClient()
	if err != nil {
		log.Printf("Auth error: %v", err)
		http.Error(w, `{"error":"authentication failed"}`, http.StatusInternalServerError)
		return
	}

	credentials, err := client.FetchAllCredentials()
	if err != nil {
		log.Printf("Dashboard error: %v", err)
		http.Error(w, `{"error":"failed to fetch dashboard"}`, http.StatusInternalServerError)
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
