package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"data_factory/internal/config"
	"data_factory/internal/db"

	"github.com/lib/pq"
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

// handleDebugQuery tests the tenant-id query directly and returns diagnostic info.
// POST /api/debug/query  body: {"tenant_ids": ["170624"]}
func (s *Server) handleDebugQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantIDs []string `json:"tenant_ids"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	cfg := s.getConfig(r)
	if cfg == nil {
		writeError(w, http.StatusBadRequest, "no saved config")
		return
	}
	conn, err := db.OpenSrcMain(*cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connect: "+err.Error())
		return
	}
	defer conn.Close()

	schema := db.SchemaOf(cfg.SrcMain)
	src := fmt.Sprintf(`"%s"."thing_product"`, schema)

	results := map[string]interface{}{}

	// 1. Total rows
	var total int
	conn.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, src)).Scan(&total)
	results["total_rows"] = total

	// 2. Distinct tenant_id values (sample)
	rows, err := conn.Query(fmt.Sprintf(
		`SELECT tenant_id::text, count(*) FROM %s GROUP BY tenant_id ORDER BY count(*) DESC LIMIT 10`, src))
	if err != nil {
		results["tenant_sample_error"] = err.Error()
	} else {
		defer rows.Close()
		sample := []map[string]interface{}{}
		for rows.Next() {
			var tid sql.NullString
			var cnt int
			rows.Scan(&tid, &cnt)
			sample = append(sample, map[string]interface{}{"tenant_id": tid.String, "count": cnt})
		}
		results["tenant_sample"] = sample
	}

	// 3. Filtered count using pq.Array + ::text[]
	if len(req.TenantIDs) > 0 {
		var filteredCount int
		err := conn.QueryRow(
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id::text = ANY($1::text[])`, src),
			pq.Array(req.TenantIDs),
		).Scan(&filteredCount)
		if err != nil {
			results["filtered_count_error"] = err.Error()
		} else {
			results["filtered_count"] = filteredCount
		}

		// 4. Raw comparison test
		var directCount int
		err = conn.QueryRow(
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1`, src),
			req.TenantIDs[0],
		).Scan(&directCount)
		if err != nil {
			results["direct_count_error"] = err.Error()
		} else {
			results["direct_count"] = directCount
		}
	}
	results["tenant_ids_received"] = req.TenantIDs

	writeJSON(w, http.StatusOK, results)
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
