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
// It gracefully handles different column name conventions (name / tenant_name / code).
func ListTenants(db *sql.DB) ([]Tenant, error) {
	// Probe available name columns to be resilient against schema variations.
	nameCol, err := probeTenantNameCol(db)
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`SELECT id::text, %s::text FROM sys_tenant ORDER BY %s`, nameCol, nameCol)
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("query sys_tenant: %w", err)
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

// probeTenantNameCol tries to find the best "display name" column in sys_tenant.
func probeTenantNameCol(db *sql.DB) (string, error) {
	const q = `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_name = 'sys_tenant'
		ORDER BY ordinal_position`
	rows, err := db.Query(q)
	if err != nil {
		return "", fmt.Errorf("probe tenant columns: %w", err)
	}
	defer rows.Close()

	preferred := []string{"tenant_name", "name", "code", "tenant_code"}
	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return "", err
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	colSet := make(map[string]bool, len(cols))
	for _, c := range cols {
		colSet[c] = true
	}
	for _, p := range preferred {
		if colSet[p] {
			return p, nil
		}
	}
	if len(cols) >= 2 {
		return cols[1], nil // fallback: second column after id
	}
	return "id", nil
}
