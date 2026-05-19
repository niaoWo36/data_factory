package db

import (
	"database/sql"
	"fmt"
)

// Tenant represents a row from sys_tenant.
type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListTenants reads sys_tenant from the given database and returns the list of tenants.
// It gracefully handles different column name conventions.
func ListTenants(db *sql.DB) ([]Tenant, error) {
	cols, err := probeTenantCols(db)
	if err != nil {
		return nil, err
	}
	idCol := pickTenantCol(cols, []string{"tenant_id", "tenant_code", "code", "id"}, "id")
	nameCol := pickTenantCol(cols, []string{"tenant_name", "name", "tenant_id"}, idCol)
	q := fmt.Sprintf(`SELECT %s::text, %s::text FROM sys_tenant ORDER BY %s`, idCol, nameCol, nameCol)
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenants []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

// probeTenantCols returns the visible sys_tenant column names in ordinal order.
func probeTenantCols(db *sql.DB) ([]string, error) {
	const q = `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'sys_tenant'
		  AND table_schema = current_schema()
		ORDER BY ordinal_position`
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("probe tenant columns: %w", err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func pickTenantCol(cols []string, preferred []string, fallback string) string {
	colSet := make(map[string]bool, len(cols))
	for _, c := range cols {
		colSet[c] = true
	}
	for _, p := range preferred {
		if colSet[p] {
			return p
		}
	}
	if len(cols) > 0 {
		return cols[0]
	}
	return fallback
}
