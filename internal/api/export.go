package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"

	"data_factory/internal/config"
	"data_factory/internal/db"
	"data_factory/internal/export"

	"github.com/gorilla/mux"
)

var exportCounter int64

// handleExportSQL generates an SQL export file and returns a download link.
func (s *Server) handleExportSQL(w http.ResponseWriter, r *http.Request) {
	var opts config.ExportOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	opts.Config.SameDB = db.IsSameDB(opts.Config)
	cfg := opts.Config

	// Open connections only when needed.
	var srcMain, dstMain, tsConn *sql.DB
	var err error

	if opts.IncludeMain || opts.IncludeTS {
		srcMain, err = db.OpenSrcMain(cfg)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "connect src_main: "+err.Error())
			return
		}
		defer srcMain.Close()

		dstMain = srcMain
		if !cfg.SameDB && opts.IncludeMain {
			dstMain, err = db.OpenDstMain(cfg)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "connect dst_main: "+err.Error())
				return
			}
			defer dstMain.Close()
		}
	}

	if opts.IncludeTS {
		tsConn, err = db.OpenSrcTS(cfg)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "connect src_ts: "+err.Error())
			return
		}
		defer tsConn.Close()
	}

	sqlContent, err := export.GenerateSQL(
		srcMain, dstMain, tsConn, srcMain, cfg,
		opts.TenantIDs,
		opts.IncludeMain, opts.IncludeTS, opts.IncludeData,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate sql: "+err.Error())
		return
	}

	exportID := fmt.Sprintf("export_%d_%s", atomic.AddInt64(&exportCounter, 1), timestamp())
	filePath := s.exportFilePath(exportID)
	if err := os.WriteFile(filePath, []byte(sqlContent), 0600); err != nil {
		writeError(w, http.StatusInternalServerError, "write file: "+err.Error())
		return
	}
	s.exports.Store(exportID, filePath)
	writeJSON(w, http.StatusOK, map[string]string{
		"export_id":    exportID,
		"download_url": "/api/export/download/" + exportID,
	})
}

// handleExportDownload serves the generated SQL file for download.
func (s *Server) handleExportDownload(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	v, ok := s.exports.Load(id)
	if !ok {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	filePath := v.(string)

	setCORSHeaders(w)
	w.Header().Set("Content-Type", "application/sql")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.sql"`, id))
	http.ServeFile(w, r, filePath)
}

// handleTimescaleCheck checks whether the TimescaleDB extension is installed
// on the destination time-series database.
// Accepts either a full AppConfig or a minimal object with just dst_ts filled in.
func (s *Server) handleTimescaleCheck(w http.ResponseWriter, r *http.Request) {
	var cfg config.AppConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	// Connect directly via dst_ts when it carries its own host; otherwise use
	// the same-db logic which falls back to src_main's host.
	var conn *sql.DB
	var err error
	if cfg.DstTS.Host != "" {
		conn, err = db.Open(cfg.DstTS)
	} else {
		cfg.SameDB = db.IsSameDB(cfg)
		conn, err = db.OpenDstTS(cfg)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connect: "+err.Error())
		return
	}
	defer conn.Close()

	installed, err := db.CheckTimescaleDB(conn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "check: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"timescaledb_installed": installed})
}
