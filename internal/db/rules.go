package db

import (
	"strings"

	"github.com/lib/pq"
)

// SchemaOnlyTables lists tables whose data must NOT be migrated / exported —
// only their DDL (table structure) is needed.
var SchemaOnlyTables = map[string]bool{
	"sys_logininfor":        true,
	"sys_oper_log":          true,
	"compute_history_value": true,
	"document_chunks":       true,
	"sj_job_log_message":    true,
	"sj_job_task":           true,
	"sj_job_task_batch":     true,
	"ele_day_value":         true,
}

// BaseTenantTables lists tables that must always include the data of the base
// tenant "000000" in addition to the user-selected tenants.
var BaseTenantTables = map[string]bool{
	"sys_menu":       true,
	"sys_config":     true,
	"sys_oss_config": true,
	"sys_tenant":     true,
	"sys_user":       true,
	"sys_role":       true,
	"sys_dict_type":  true,
	"sys_dict_data":  true,
}

const baseTenantID = "000000"

// EffectiveTenantIDs returns the tenant ID list to use when querying a specific
// table. For BaseTenantTables, "000000" is always appended (deduplicated).
// An empty input means ALL tenants (no filtering) and stays empty so callers
// can distinguish "no filter" from "filter by 000000 only".
func EffectiveTenantIDs(table string, tenantIDs []string) []string {
	trimmed := trimTenantIDs(tenantIDs)
	if len(trimmed) == 0 {
		return nil
	}
	if !BaseTenantTables[table] {
		return trimmed
	}
	// Check whether 000000 is already present.
	for _, id := range trimmed {
		if id == baseTenantID {
			return trimmed
		}
	}
	return append(trimmed, baseTenantID)
}

// trimTenantIDs trims whitespace around each ID and drops empties.
func trimTenantIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := strings.TrimSpace(id); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// expandTenantVariants returns the filter values to match against
// btrim(tenant_id::text). Besides the IDs themselves it includes the
// leading-zero-stripped form of numeric IDs so that e.g. filter "000000"
// also matches an integer tenant_id column storing 0 (whose ::text is "0").
func expandTenantVariants(ids []string) []string {
	out := make([]string, 0, len(ids)*2)
	seen := make(map[string]bool, len(ids)*2)
	for _, id := range ids {
		t := strings.TrimSpace(id)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if stripped := strings.TrimLeft(t, "0"); stripped != t && isDigits(t) {
			if stripped == "" {
				stripped = "0"
			}
			if !seen[stripped] {
				seen[stripped] = true
				out = append(out, stripped)
			}
		}
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// TenantFilterClause is the WHERE fragment used by every tenant-filtered data
// query. It keeps rows whose tenant_id is NULL (shared/unpartitioned data),
// tolerates char(n) padding via btrim, and tolerates int-vs-text mismatches
// (e.g. integer 0 vs filter "000000") because $1 carries the expanded
// variants built by TenantFilterArgs.
const TenantFilterClause = `(tenant_id IS NULL OR btrim(tenant_id::text) = ANY($1::text[]))`

// TenantFilterArgs builds the query argument for tenantFilterClause.
func TenantFilterArgs(table string, tenantIDs []string) interface{} {
	return pq.Array(expandTenantVariants(EffectiveTenantIDs(table, tenantIDs)))
}
