package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"data_factory/internal/config"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Server holds shared state for the HTTP API.
type Server struct {
	mu      sync.Mutex
	session *config.AppConfig // last saved connection config

	tasks   sync.Map // taskID -> *MigrateTask
	exports sync.Map // exportID -> filePath

	exportDir  string // temp directory for exported SQL files
	configFile string // path to persisted config JSON file

	upgrader websocket.Upgrader
}

// NewServer initialises a Server with a temp dir for exports and loads any saved config.
func NewServer(configFile string) (*Server, error) {
	dir, err := os.MkdirTemp("", "data_factory_exports_*")
	if err != nil {
		return nil, err
	}
	srv := &Server{
		exportDir:  dir,
		configFile: configFile,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
	srv.loadConfigFromFile()
	return srv, nil
}

// RegisterRoutes wires all API endpoints onto router r.
func (s *Server) RegisterRoutes(r *mux.Router) {
	api := r.PathPrefix("/api").Subrouter()

	// Connection / config
	api.HandleFunc("/config/test-connection", s.handleTestConnection).Methods(http.MethodPost, http.MethodOptions)
	api.HandleFunc("/config/save", s.handleSaveConfig).Methods(http.MethodPost, http.MethodOptions)
	api.HandleFunc("/config/load", s.handleLoadConfig).Methods(http.MethodGet, http.MethodOptions)

	// Tenants
	api.HandleFunc("/tenants", s.handleListTenants).Methods(http.MethodGet, http.MethodOptions)

	// Migration
	api.HandleFunc("/migrate/start", s.handleMigrateStart).Methods(http.MethodPost, http.MethodOptions)
	api.HandleFunc("/migrate/progress", s.handleMigrateProgress) // WebSocket
	api.HandleFunc("/migrate/status", s.handleMigrateStatus).Methods(http.MethodGet, http.MethodOptions)

	// Export
	api.HandleFunc("/export/sql", s.handleExportSQL).Methods(http.MethodPost, http.MethodOptions)
	api.HandleFunc("/export/download/{id}", s.handleExportDownload).Methods(http.MethodGet, http.MethodOptions)

	// TimescaleDB
	api.HandleFunc("/timescale/check", s.handleTimescaleCheck).Methods(http.MethodPost, http.MethodOptions)

	// CORS preflight
	api.Methods(http.MethodOptions).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		w.WriteHeader(http.StatusNoContent)
	})
}

// Cleanup removes the temporary export directory.
func (s *Server) Cleanup() {
	os.RemoveAll(s.exportDir)
}

// exportFilePath returns the full file path for an export with the given ID.
func (s *Server) exportFilePath(id string) string {
	return filepath.Join(s.exportDir, id+".sql")
}

// --- helpers ---

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	setCORSHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// timestamp returns a sortable timestamp string suitable for filenames.
func timestamp() string {
	return time.Now().Format("20060102_150405")
}
