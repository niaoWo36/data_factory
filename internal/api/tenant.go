package api

import (
	"encoding/json"
	"net/http"

	"data_factory/internal/config"
	"data_factory/internal/db"
)

// handleListTenants returns the list of tenants from the source main database.
// The config can be passed in the request body, or the saved session config is used.
func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	cfg := s.getConfig(r)
	if cfg == nil {
		writeError(w, http.StatusBadRequest, "no database configuration provided")
		return
	}

	conn, err := db.OpenSrcMain(*cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connect: "+err.Error())
		return
	}
	defer conn.Close()

	tenants, err := db.ListTenants(conn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tenants: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tenants": tenants})
}

// getConfig tries to read an AppConfig from the request body; falls back to the
// session-saved config.
func (s *Server) getConfig(r *http.Request) *config.AppConfig {
	// Try body first.
	if r.Body != nil && r.ContentLength != 0 {
		var cfg config.AppConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err == nil && cfg.SrcMain.Host != "" {
			cfg.SameDB = db.IsSameDB(cfg)
			return &cfg
		}
	}
	// Fall back to session.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		cp := *s.session
		return &cp
	}
	return nil
}
