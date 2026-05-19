package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Progress is emitted during a migration to report status.
type Progress struct {
	Stage   string `json:"stage"`   // schema | data | timeseries | fk | index
	Table   string `json:"table"`
	Message string `json:"message"`
	Done    int    `json:"done"`    // rows / tables processed so far
	Total   int    `json:"total"`   // total rows / tables (-1 if unknown)
	Error   string `json:"error,omitempty"`
}

// ProgressFunc is called with each progress update.
type ProgressFunc func(p Progress)

const batchSize = 1000

// MigrateSchema copies the table structures (DDL) from srcDB/srcSchema to
// dstDB/dstSchema. Foreign keys are deferred and added after all tables exist.
func MigrateSchema(ctx context.Context, srcDB, dstDB *sql.DB, srcSchema, dstSchema string, progress ProgressFunc) error {
	tables, err := ListTables(srcDB, srcSchema)
	if err != nil {
		return err
	}
	progress(Progress{Stage: "schema", Message: fmt.Sprintf("Found %d tables", len(tables)), Total: len(tables)})

	// Ensure target schema exists.
	if _, err := dstDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdent(dstSchema))); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	// Create sequences first so that column defaults (nextval) resolve correctly.
	seqs, err := ListSequences(srcDB, srcSchema)
	if err != nil {
		return fmt.Errorf("list sequences: %w", err)
	}
	for _, seq := range seqs {
		ddl := CreateSequenceDDL(seq, dstSchema)
		if _, err := dstDB.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create sequence %s: %w", seq.Name, err)
		}
	}
	if len(seqs) > 0 {
		progress(Progress{Stage: "schema", Message: fmt.Sprintf("Created %d sequences", len(seqs))})
	}

	infos := make([]*TableInfo, 0, len(tables))
	for _, t := range tables {
		info, err := IntrospectTable(srcDB, srcSchema, t)
		if err != nil {
			return fmt.Errorf("introspect %s: %w", t, err)
		}
		infos = append(infos, info)
	}

	// Create tables (without FKs).
	for i, info := range infos {
		if err := ctx.Err(); err != nil {
			return err
		}
		ddl := CreateTableDDL(info, dstSchema)
		if _, err := dstDB.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create table %s: %w\nDDL: %s", info.Name, err, ddl)
		}
		progress(Progress{Stage: "schema", Table: info.Name,
			Message: fmt.Sprintf("Created table %s", info.Name), Done: i + 1, Total: len(infos)})
	}

	// Create indexes.
	for _, info := range infos {
		for _, stmt := range IndexDDL(info, srcSchema, dstSchema) {
			if _, err := dstDB.ExecContext(ctx, stmt); err != nil {
				// Non-fatal: log and continue.
				progress(Progress{Stage: "index", Table: info.Name,
					Message: fmt.Sprintf("index warn: %v", err)})
			}
		}
	}

	// Add foreign keys last.
	for _, info := range infos {
		for _, stmt := range ForeignKeyDDL(info, srcSchema, dstSchema) {
			if _, err := dstDB.ExecContext(ctx, stmt); err != nil {
				progress(Progress{Stage: "fk", Table: info.Name,
					Message: fmt.Sprintf("fk warn: %v", err)})
			}
		}
	}

	progress(Progress{Stage: "schema", Message: "Schema migration complete", Done: len(infos), Total: len(infos)})
	return nil
}

// MigrateData copies rows for the given tenantIDs from srcDB to dstDB.
// When a table has no tenant_id column, all rows are copied.
// Same-database migrations copy directly between schemas via qualified names.
func MigrateData(ctx context.Context, srcDB, dstDB *sql.DB, srcSchema, dstSchema string,
	tenantIDs []string, sameDB bool, progress ProgressFunc) error {

	tables, err := ListTables(srcDB, srcSchema)
	if err != nil {
		return err
	}
	progress(Progress{Stage: "data", Message: fmt.Sprintf("Migrating data for %d tables", len(tables)), Total: len(tables)})

	for i, table := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := IntrospectTable(srcDB, srcSchema, table)
		if err != nil {
			return fmt.Errorf("introspect %s: %w", table, err)
		}

		if sameDB {
			if err := migrateDataSameDB(ctx, srcDB, info, srcSchema, dstSchema, tenantIDs, progress); err != nil {
				return err
			}
		} else {
			if err := migrateDataCrossDB(ctx, srcDB, dstDB, info, srcSchema, dstSchema, tenantIDs, progress); err != nil {
				return err
			}
		}
		progress(Progress{Stage: "data", Table: table,
			Message: fmt.Sprintf("Table %s done", table), Done: i + 1, Total: len(tables)})
	}

	progress(Progress{Stage: "data", Message: "Data migration complete", Done: len(tables), Total: len(tables)})
	return nil
}

// migrateDataSameDB uses INSERT ... SELECT ... within the same database for efficiency.
func migrateDataSameDB(ctx context.Context, db *sql.DB, info *TableInfo,
	srcSchema, dstSchema string, tenantIDs []string, progress ProgressFunc) error {

	colList := columnList(info.Columns)
	src := fmt.Sprintf("%s.%s", quoteIdent(srcSchema), quoteIdent(info.Name))
	dst := fmt.Sprintf("%s.%s", quoteIdent(dstSchema), quoteIdent(info.Name))

	var q string
	if info.HasTenantID && len(tenantIDs) > 0 {
		q = fmt.Sprintf(
			`INSERT INTO %s (%s) SELECT %s FROM %s WHERE tenant_id = ANY($1::text[]) ON CONFLICT DO NOTHING`,
			dst, colList, colList, src)
		_, err := db.ExecContext(ctx, q, pqArray(tenantIDs))
		return err
	}
	q = fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM %s ON CONFLICT DO NOTHING`, dst, colList, colList, src)
	_, err := db.ExecContext(ctx, q)
	return err
}

// migrateDataCrossDB uses server-side cursors and batched inserts to move data
// between two separate PostgreSQL instances.
func migrateDataCrossDB(ctx context.Context, srcDB, dstDB *sql.DB, info *TableInfo,
	srcSchema, dstSchema string, tenantIDs []string, progress ProgressFunc) error {

	colList := columnList(info.Columns)
	src := fmt.Sprintf("%s.%s", quoteIdent(srcSchema), quoteIdent(info.Name))

	var selectQ string
	var args []interface{}
	if info.HasTenantID && len(tenantIDs) > 0 {
		selectQ = fmt.Sprintf(`SELECT %s FROM %s WHERE tenant_id = ANY($1::text[])`, colList, src)
		args = []interface{}{pqArray(tenantIDs)}
	} else {
		selectQ = fmt.Sprintf(`SELECT %s FROM %s`, colList, src)
	}

	rows, err := srcDB.QueryContext(ctx, selectQ, args...)
	if err != nil {
		return fmt.Errorf("select from %s: %w", info.Name, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	dst := fmt.Sprintf("%s.%s", quoteIdent(dstSchema), quoteIdent(info.Name))
	placeholders := makePlaceholders(len(cols))
	insertQ := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING`, dst, colList, placeholders)

	var rowBuf [][]interface{}
	flush := func() error {
		tx, err := dstDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, insertQ)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer stmt.Close()
		for _, row := range rowBuf {
			if _, err := stmt.ExecContext(ctx, row...); err != nil {
				tx.Rollback()
				return fmt.Errorf("insert into %s: %w", info.Name, err)
			}
		}
		rowBuf = rowBuf[:0]
		return tx.Commit()
	}

	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	total := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]interface{}, len(cols))
		copy(row, vals)
		rowBuf = append(rowBuf, row)
		total++
		if len(rowBuf) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
			progress(Progress{Stage: "data", Table: info.Name,
				Message: fmt.Sprintf("Inserted %d rows", total), Done: total, Total: -1})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(rowBuf) > 0 {
		if err := flush(); err != nil {
			return err
		}
	}
	progress(Progress{Stage: "data", Table: info.Name,
		Message: fmt.Sprintf("Inserted %d rows total", total), Done: total, Total: total})
	return nil
}

// --- helpers ---

func columnList(cols []Column) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quoteIdent(c.Name)
	}
	return strings.Join(names, ", ")
}

func makePlaceholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

// pqArray wraps a []string into a value that lib/pq recognises as a text[] parameter.
func pqArray(ss []string) interface{} {
	// Build a PostgreSQL array literal: '{"a","b","c"}'
	escaped := make([]string, len(ss))
	for i, s := range ss {
		escaped[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "{" + strings.Join(escaped, ",") + "}"
}
