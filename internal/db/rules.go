package db

// SchemaOnlyTables lists tables whose data must NOT be migrated / exported —
// only their DDL (table structure) is needed.
var SchemaOnlyTables = map[string]bool{
	"sys_logininfor":       true,
	"sys_oper_log":         true,
	"compute_history_value": true,
	"document_chunks":      true,
	"sj_job_log_message":   true,
	"sj_job_task":          true,
	"sj_job_task_batch":    true,
	"ele_day_value":        true,
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
// table.  For BaseTenantTables, "000000" is always appended (deduplicated).
func EffectiveTenantIDs(table string, tenantIDs []string) []string {
	if !BaseTenantTables[table] {
		return tenantIDs
	}
	// Check whether 000000 is already present.
	for _, id := range tenantIDs {
		if id == baseTenantID {
			return tenantIDs
		}
	}
	out := make([]string, len(tenantIDs)+1)
	copy(out, tenantIDs)
	out[len(tenantIDs)] = baseTenantID
	return out
}
