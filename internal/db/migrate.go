package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// Progress is emitted during a migration to report status.
type Progress struct {
	Stage   string `json:"stage"` // schema | data | timeseries | fk | index
	Table   string `json:"table"`
	Message string `json:"message"`
	Done    int    `json:"done"`  // rows / tables processed so far
	Total   int    `json:"total"` // total rows / tables (-1 if unknown)
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

	// Sync PostgreSQL extensions (e.g. pgvector, postgis) from source to target.
	// Failures are non-fatal – we warn and tables that depend on missing extensions
	// will be skipped below rather than aborting the whole migration.
	skippedExts := syncExtensions(ctx, srcDB, dstDB)
	if len(skippedExts) > 0 {
		progress(Progress{Stage: "schema",
			Message: fmt.Sprintf("⚠ Extensions not available on target (tables using their types will be skipped): %s",
				strings.Join(skippedExts, ", "))})
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

	// Create tables (without FKs). Failures are non-fatal: warn and skip so that
	// a missing extension (e.g. pgvector) does not abort the entire migration.
	skippedTables := make(map[string]bool)
	for i, info := range infos {
		if err := ctx.Err(); err != nil {
			return err
		}
		ddl := CreateTableDDL(info, dstSchema)
		if _, err := dstDB.ExecContext(ctx, ddl); err != nil {
			skippedTables[info.Name] = true
			progress(Progress{Stage: "schema", Table: info.Name,
				Message: fmt.Sprintf("⚠ Skipped table %s: %v", info.Name, err),
				Done:    i + 1, Total: len(infos)})
			continue
		}
		progress(Progress{Stage: "schema", Table: info.Name,
			Message: fmt.Sprintf("Created table %s", info.Name), Done: i + 1, Total: len(infos)})
	}

	// Create indexes (skip tables that failed above).
	for _, info := range infos {
		if skippedTables[info.Name] {
			continue
		}
		for _, stmt := range IndexDDL(info, srcSchema, dstSchema) {
			if _, err := dstDB.ExecContext(ctx, stmt); err != nil {
				// Non-fatal: log and continue.
				progress(Progress{Stage: "index", Table: info.Name,
					Message: fmt.Sprintf("index warn: %v", err)})
			}
		}
	}

	// Add foreign keys last (skip tables that failed above).
	for _, info := range infos {
		if skippedTables[info.Name] {
			continue
		}
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
	progress(Progress{Stage: "data", Message: fmt.Sprintf("Migrating data for %d tables (tenants: %v)", len(tables), tenantIDs), Total: len(tables)})

	var failedTables []string

	for i, table := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Schema-only tables: skip data entirely.
		if SchemaOnlyTables[table] {
			progress(Progress{Stage: "data", Table: table,
				Message: fmt.Sprintf("Skipped data for %s (schema-only table)", table),
				Done:    i + 1, Total: len(tables)})
			continue
		}

		info, err := IntrospectTable(srcDB, srcSchema, table)
		if err != nil {
			failed := fmt.Sprintf("%s(introspect: %v)", table, err)
			failedTables = append(failedTables, failed)
			progress(Progress{Stage: "data", Table: table,
				Message: fmt.Sprintf("⚠ Skipped (introspect failed): %v", err), Done: i + 1, Total: len(tables)})
			continue
		}

		// For base-tenant tables always include tenant 000000.
		effectiveTenants := EffectiveTenantIDs(table, tenantIDs)

		var dataErr error
		if sameDB {
			dataErr = migrateDataSameDB(ctx, srcDB, info, srcSchema, dstSchema, effectiveTenants, progress)
		} else {
			dataErr = migrateDataCrossDB(ctx, srcDB, dstDB, info, srcSchema, dstSchema, effectiveTenants, progress)
		}
		if dataErr != nil {
			failedTables = append(failedTables, fmt.Sprintf("%s(%v)", table, dataErr))
			progress(Progress{Stage: "data", Table: table,
				Message: fmt.Sprintf("⚠ Skipped data for %s: %v", table, dataErr), Done: i + 1, Total: len(tables)})
			continue
		}
		progress(Progress{Stage: "data", Table: table,
			Message: fmt.Sprintf("Table %s done", table), Done: i + 1, Total: len(tables)})
	}

	if seqFailed := syncSequenceValues(ctx, srcDB, dstDB, srcSchema, dstSchema, progress); len(seqFailed) > 0 {
		failedTables = append(failedTables, seqFailed...)
	}

	if len(failedTables) > 0 {
		progress(Progress{Stage: "data",
			Message: fmt.Sprintf("Data migration complete with %d failures: %s", len(failedTables), strings.Join(failedTables, "; ")),
			Done:    len(tables), Total: len(tables)})
		return fmt.Errorf("data migration incomplete (%d failed): %s", len(failedTables), strings.Join(failedTables, "; "))
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
	filterMsg := "Copying all rows"
	if info.HasTenantID && len(tenantIDs) > 0 {
		filterMsg = fmt.Sprintf("Filtering tenant rows: %v", tenantIDs)
		progress(Progress{Stage: "data", Table: info.Name, Message: filterMsg})
		q = fmt.Sprintf(
			`INSERT INTO %s (%s) SELECT %s FROM %s WHERE %s ON CONFLICT DO NOTHING`,
			dst, colList, colList, src, TenantFilterClause)
		res, err := db.ExecContext(ctx, q, TenantFilterArgs(info.Name, tenantIDs))
		if err != nil {
			return err
		}
		if rows, rowsErr := res.RowsAffected(); rowsErr == nil {
			progress(Progress{Stage: "data", Table: info.Name,
				Message: fmt.Sprintf("Inserted %d rows total", rows), Done: int(rows), Total: int(rows)})
		}
		return nil
	}
	progress(Progress{Stage: "data", Table: info.Name, Message: filterMsg})
	q = fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM %s ON CONFLICT DO NOTHING`, dst, colList, colList, src)
	res, err := db.ExecContext(ctx, q)
	if err != nil {
		return err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil {
		progress(Progress{Stage: "data", Table: info.Name,
			Message: fmt.Sprintf("Inserted %d rows total", rows), Done: int(rows), Total: int(rows)})
	}
	return nil
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
		progress(Progress{Stage: "data", Table: info.Name,
			Message: fmt.Sprintf("Filtering tenant rows: %v", tenantIDs)})
		selectQ = fmt.Sprintf(`SELECT %s FROM %s WHERE %s`, colList, src, TenantFilterClause)
		args = []interface{}{TenantFilterArgs(info.Name, tenantIDs)}
	} else {
		progress(Progress{Stage: "data", Table: info.Name, Message: "Copying all rows"})
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
	insertedTotal := 0
	flush := func() error {
		tx, err := dstDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				tx.Rollback()
			}
		}()
		stmt, err := tx.PrepareContext(ctx, insertQ)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, row := range rowBuf {
			res, err := stmt.ExecContext(ctx, row...)
			if err != nil {
				return fmt.Errorf("insert into %s: %w", info.Name, err)
			}
			if affected, rowsErr := res.RowsAffected(); rowsErr == nil {
				insertedTotal += int(affected)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s batch (%d rows): %w", info.Name, len(rowBuf), err)
		}
		committed = true
		rowBuf = rowBuf[:0]
		return nil
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
				Message: fmt.Sprintf("Scanned %d rows, inserted %d rows", total, insertedTotal), Done: total, Total: -1})
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
		Message: fmt.Sprintf("Scanned %d rows total, inserted %d rows total", total, insertedTotal), Done: total, Total: total})
	return nil
}

// --- helpers ---

// syncSequenceValues copies sequence last_value/is_called from source to
// destination so that subsequent inserts on the target do not reuse existing
// keys (which would then be swallowed by ON CONFLICT DO NOTHING and look like
// lost new data). Returns descriptions of sequences that could not be synced.
func syncSequenceValues(ctx context.Context, srcDB, dstDB *sql.DB, srcSchema, dstSchema string, progress ProgressFunc) []string {
	seqs, err := ListSequences(srcDB, srcSchema)
	if err != nil {
		return []string{fmt.Sprintf("sequences(list: %v)", err)}
	}
	var failed []string
	for _, seq := range seqs {
		var lastVal int64
		var isCalled bool
		q := fmt.Sprintf(`SELECT last_value, is_called FROM %s.%s`, quoteIdent(srcSchema), quoteIdent(seq.Name))
		if err := srcDB.QueryRowContext(ctx, q).Scan(&lastVal, &isCalled); err != nil {
			failed = append(failed, fmt.Sprintf("%s(read: %v)", seq.Name, err))
			continue
		}
		setQ := fmt.Sprintf(`SELECT setval('%s.%s', $1, $2)`,
			strings.ReplaceAll(dstSchema, `'`, `''`), strings.ReplaceAll(seq.Name, `'`, `''`))
		if _, err := dstDB.ExecContext(ctx, setQ, lastVal, isCalled); err != nil {
			failed = append(failed, fmt.Sprintf("%s(setval: %v)", seq.Name, err))
			continue
		}
	}
	if len(seqs) > 0 && len(failed) == 0 {
		progress(Progress{Stage: "data", Message: fmt.Sprintf("Synced %d sequence values", len(seqs))})
	}
	return failed
}

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

// tenantArray returns a pq.Array value suitable for tenant-filter queries.
// Kept for compatibility; new code should use TenantFilterArgs.
func tenantArray(ids []string) interface{} {
	return pq.Array(ids)
}

// syncExtensions tries to install on dstDB every extension that is installed on
// srcDB. plpgsql is always present and skipped. Returns the names of any
// extensions that could not be created (e.g. not installed at the OS level).
func syncExtensions(ctx context.Context, srcDB, dstDB *sql.DB) []string {
	rows, err := srcDB.QueryContext(ctx, `SELECT extname FROM pg_extension ORDER BY extname`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var failed []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		if name == "plpgsql" {
			continue // always present, skip
		}
		stmt := fmt.Sprintf(`CREATE EXTENSION IF NOT EXISTS %s CASCADE`, quoteIdent(name))
		if _, err := dstDB.ExecContext(ctx, stmt); err != nil {
			failed = append(failed, name)
		}
	}
	return failed
}
