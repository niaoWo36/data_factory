package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"data_factory/internal/config"
	"data_factory/internal/db"
)

// handleTestConnection tests one or all four database connections.
// Request body: config.AppConfig  (or a subset with only the connection to test)
// Response: { "ok": true } | { "error": "..." }
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var cfg config.AppConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Detect same-database flag automatically.
	cfg.SameDB = db.IsSameDB(cfg)

	results := map[string]interface{}{}

	// Test source main DB.
	if cfg.SrcMain.Host != "" {
		conn, err := db.OpenSrcMain(cfg)
		if err != nil {
			results["src_main"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			conn.Close()
			results["src_main"] = map[string]interface{}{"ok": true}
		}
	}

	// Test source TS DB (same host as main).
	if cfg.SrcTS.Schema != "" {
		conn, err := db.OpenSrcTS(cfg)
		if err != nil {
			results["src_ts"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			conn.Close()
			results["src_ts"] = map[string]interface{}{"ok": true}
		}
	}

	// Test destination main DB.
	if cfg.DstMain.Host != "" || (cfg.SameDB && cfg.DstMain.Schema != "") {
		conn, err := db.OpenDstMain(cfg)
		if err != nil {
			results["dst_main"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			conn.Close()
			results["dst_main"] = map[string]interface{}{"ok": true}
		}
	}

	// Test destination TS DB (always shares DstMain server, different schema).
	if cfg.DstTS.Schema != "" {
		conn, err := db.OpenDstTS(cfg)
		if err != nil {
			results["dst_ts"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			conn.Close()
			results["dst_ts"] = map[string]interface{}{"ok": true}
		}
	}

	results["same_db"] = cfg.SameDB
	writeJSON(w, http.StatusOK, results)
}

// handleSaveConfig persists the connection configuration in the server's session and on disk.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.AppConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	cfg.SameDB = db.IsSameDB(cfg)

	s.mu.Lock()
	s.session = &cfg
	s.mu.Unlock()

	s.saveConfigToFile(&cfg)

	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "same_db": cfg.SameDB})
}

// handleLoadConfig returns the last saved configuration (from file or in-memory session).
func (s *Server) handleLoadConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.session
	s.mu.Unlock()

	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "config": cfg})
}

// loadConfigFromFile reads a previously saved config from disk into the session.
func (s *Server) loadConfigFromFile() {
	if s.configFile == "" {
		return
	}
	data, err := os.ReadFile(s.configFile)
	if err != nil {
		return // file doesn't exist yet — that's fine
	}
	var cfg config.AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("warn: failed to parse config file %s: %v", s.configFile, err)
		return
	}
	s.mu.Lock()
	s.session = &cfg
	s.mu.Unlock()
	log.Printf("loaded config from %s", s.configFile)
}

// saveConfigToFile persists the config to disk.
func (s *Server) saveConfigToFile(cfg *config.AppConfig) {
	if s.configFile == "" {
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Printf("warn: failed to marshal config: %v", err)
		return
	}
	if err := os.WriteFile(s.configFile, data, 0600); err != nil {
		log.Printf("warn: failed to write config file %s: %v", s.configFile, err)
	}
}
