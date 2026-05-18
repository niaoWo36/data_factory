package api

import (
	"encoding/json"
	"net/http"

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
	if cfg.DstMain.Host != "" || (cfg.SameDB && cfg.DstMain.DBName != "") {
		conn, err := db.OpenDstMain(cfg)
		if err != nil {
			results["dst_main"] = map[string]interface{}{"ok": false, "error": err.Error()}
		} else {
			conn.Close()
			results["dst_main"] = map[string]interface{}{"ok": true}
		}
	}

	// Test destination TS DB.
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

// handleSaveConfig persists the connection configuration in the server's session.
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

	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "same_db": cfg.SameDB})
}
