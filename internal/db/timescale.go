package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// TSTableName is the name of a time-series (hypertable) table.
type TSTableName = string

// CheckTimescaleDB returns true if the TimescaleDB extension is installed in the database.
func CheckTimescaleDB(db *sql.DB) (bool, error) {
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&exists)
	return exists, err
}

// InitTimescaleDB installs the TimescaleDB extension if it is not already present.
// It also creates the target schema if needed.
func InitTimescaleDB(ctx context.Context, db *sql.DB, schema string) error {
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE`); err != nil {
		return fmt.Errorf("create extension timescaledb: %w", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdent(schema))); err != nil {
		return fmt.Errorf("create ts schema: %w", err)
	}
	return nil
}

// GetTSTables queries the thing_product table in the main database to find
// the iot_table values (time-series table names) for the given tenant IDs.
// The cast tenant_id::text ensures the comparison works regardless of whether
// the column is stored as text, varchar, or a numeric type.
func GetTSTables(mainDB *sql.DB, mainSchema string, tenantIDs []string) ([]TSTableName, error) {
	src := fmt.Sprintf("%s.%s", quoteIdent(mainSchema), quoteIdent("thing_product"))
	var q string
	var args []interface{}
	if len(tenantIDs) > 0 {
		q = fmt.Sprintf(`
			SELECT DISTINCT iot_table
			FROM %s
			WHERE iot_table IS NOT NULL AND trim(iot_table) != ''
			  AND tenant_id::text = ANY($1::text[])
			ORDER BY iot_table`, src)
		args = []interface{}{pq.Array(tenantIDs)}
	} else {
		q = fmt.Sprintf(`
			SELECT DISTINCT iot_table
			FROM %s
			WHERE iot_table IS NOT NULL AND trim(iot_table) != ''
			ORDER BY iot_table`, src)
	}

	rows, err := mainDB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("get ts tables: %w", err)
	}
	defer rows.Close()

	var tables []TSTableName
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		// Strip any schema prefix (e.g. "iot.table_name" → "table_name")
		if dot := strings.LastIndex(t, "."); dot >= 0 {
			t = t[dot+1:]
		}
		t = strings.Trim(t, `"`)
		if t != "" {
			tables = append(tables, t)
		}
	}
	return tables, rows.Err()
}

// DiagnoseTSTables returns a human-readable summary of thing_product rows and
// iot_table values for the given tenants. Used when GetTSTables finds nothing.
func DiagnoseTSTables(mainDB *sql.DB, mainSchema string, tenantIDs []string) string {
	src := fmt.Sprintf("%s.%s", quoteIdent(mainSchema), quoteIdent("thing_product"))

	// Total rows for tenant.
	var totalRows int
	countQ := fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id::text = ANY($1::text[])`, src)
	if len(tenantIDs) == 0 {
		countQ = fmt.Sprintf(`SELECT count(*) FROM %s`, src)
	}
	_ = mainDB.QueryRow(countQ, pq.Array(tenantIDs)).Scan(&totalRows)

	// Distinct iot_table values (including NULLs) for the tenant.
	sampleQ := fmt.Sprintf(`
		SELECT iot_table, count(*) as cnt
		FROM %s
		WHERE tenant_id::text = ANY($1::text[])
		GROUP BY iot_table
		ORDER BY cnt DESC
		LIMIT 5`, src)
	rows, err := mainDB.Query(sampleQ, pq.Array(tenantIDs))
	if err != nil {
		return fmt.Sprintf("thing_product total=%d; could not sample iot_table: %v", totalRows, err)
	}
	defer rows.Close()

	var samples []string
	for rows.Next() {
		var val sql.NullString
		var cnt int
		if rows.Scan(&val, &cnt) == nil {
			v := "NULL"
			if val.Valid {
				v = fmt.Sprintf("%q", val.String)
			}
			samples = append(samples, fmt.Sprintf("%s×%d", v, cnt))
		}
	}

	if len(samples) == 0 {
		return fmt.Sprintf("thing_product matched 0 rows for tenants %v (total rows in table: %d)", tenantIDs, totalRows)
	}
	return fmt.Sprintf("thing_product rows=%d, iot_table sample: %s", totalRows, strings.Join(samples, ", "))
}

// HypertableInfo holds the time-dimension column and chunk interval of a hypertable.
type HypertableInfo struct {
	TimeDimension     string
	ChunkTimeInterval string // interval string e.g. "7 days"
}

// GetHypertableInfo retrieves the time-dimension column and chunk interval for the given hypertable.
func GetHypertableInfo(tsDB *sql.DB, schema, table string) (*HypertableInfo, error) {
	actualTable, err := resolveTableName(tsDB, schema, table)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT d.column_name,
		       _timescaledb_functions.to_interval(d.interval_length)::text
		FROM _timescaledb_catalog.hypertable h
		JOIN _timescaledb_catalog.dimension d ON d.hypertable_id = h.id
		WHERE h.schema_name = $1 AND h.table_name = $2
		  AND d.column_type <> 'integer'::regtype
		LIMIT 1`
	var hi HypertableInfo
	err = tsDB.QueryRow(q, schema, actualTable).Scan(&hi.TimeDimension, &hi.ChunkTimeInterval)
	if err == sql.ErrNoRows {
		// Try the older API (pre-2.x)
		const q2 = `
			SELECT d.column_name,
			       _timescaledb_internal.to_interval(d.interval_length)::text
			FROM _timescaledb_catalog.hypertable h
			JOIN _timescaledb_catalog.dimension d ON d.hypertable_id = h.id
			WHERE h.schema_name = $1 AND h.table_name = $2
			  AND d.column_type <> 'integer'::regtype
			LIMIT 1`
		err2 := tsDB.QueryRow(q2, schema, actualTable).Scan(&hi.TimeDimension, &hi.ChunkTimeInterval)
		if err2 != nil {
			// Default fallback
			hi.TimeDimension = "time"
			hi.ChunkTimeInterval = "7 days"
			return &hi, nil
		}
	} else if err != nil {
		return nil, fmt.Errorf("get hypertable info %s.%s: %w", schema, actualTable, err)
	}
	return &hi, nil
}

// MigrateTimeSeries copies time-series table structures (and optionally data)
// from srcTSDB to dstTSDB for each table in tsTables.
func MigrateTimeSeries(ctx context.Context,
	srcTSDB, dstTSDB *sql.DB,
	mainDB *sql.DB,
	srcTSSchema, dstTSSchema, mainSchema string,
	tenantIDs []string,
	sameDB bool,
	progress ProgressFunc) error {

	// Ensure TimescaleDB is installed on destination.
	ok, err := CheckTimescaleDB(dstTSDB)
	if err != nil {
		return fmt.Errorf("check timescaledb: %w", err)
	}
	if !ok {
		progress(Progress{Stage: "timeseries", Message: "TimescaleDB not found on destination, initializing..."})
		if err := InitTimescaleDB(ctx, dstTSDB, dstTSSchema); err != nil {
			return err
		}
		progress(Progress{Stage: "timeseries", Message: "TimescaleDB initialized"})
	} else {
		// Even if extension exists, ensure schema is present.
		if _, err := dstTSDB.ExecContext(ctx,
			fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdent(dstTSSchema))); err != nil {
			return fmt.Errorf("create ts schema: %w", err)
		}
	}

	tsTables, err := GetTSTables(mainDB, mainSchema, tenantIDs)
	if err != nil {
		return fmt.Errorf("get ts tables: %w", err)
	}
	if len(tsTables) == 0 {
		diag := DiagnoseTSTables(mainDB, mainSchema, tenantIDs)
		progress(Progress{Stage: "timeseries", Message: "No time-series tables found for selected tenants. " + diag})
		return nil
	}
	progress(Progress{Stage: "timeseries", Message: fmt.Sprintf("Found %d TS tables", len(tsTables)), Total: len(tsTables)})

	for i, table := range tsTables {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Introspect source TS table.
		info, err := IntrospectTable(srcTSDB, srcTSSchema, table)
		if err != nil {
			progress(Progress{Stage: "timeseries", Table: table,
				Message: fmt.Sprintf("skip: introspect error: %v", err)})
			continue
		}

		// Create plain table on destination.
		actualTable := info.Name
		ddl := CreateTableDDL(info, dstTSSchema)
		if _, err := dstTSDB.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create ts table %s: %w", actualTable, err)
		}

		// Get hypertable metadata.
		hi, err := GetHypertableInfo(srcTSDB, srcTSSchema, actualTable)
		if err != nil {
			return err
		}

		// Convert to hypertable on destination.
		createHT := fmt.Sprintf(
			`SELECT create_hypertable('%s.%s', '%s', chunk_time_interval => INTERVAL '%s', if_not_exists => TRUE)`,
			dstTSSchema, actualTable, hi.TimeDimension, hi.ChunkTimeInterval)
		if _, err := dstTSDB.ExecContext(ctx, createHT); err != nil {
			return fmt.Errorf("create_hypertable %s: %w", actualTable, err)
		}

		// Migrate data.
		if sameDB {
			if err := migrateDataSameDB(ctx, srcTSDB, info, srcTSSchema, dstTSSchema, tenantIDs, progress); err != nil {
				return fmt.Errorf("migrate ts data %s: %w", actualTable, err)
			}
		} else {
			if err := migrateDataCrossDB(ctx, srcTSDB, dstTSDB, info, srcTSSchema, dstTSSchema, tenantIDs, progress); err != nil {
				return fmt.Errorf("migrate ts data %s: %w", actualTable, err)
			}
		}

		progress(Progress{Stage: "timeseries", Table: actualTable,
			Message: fmt.Sprintf("TS table %s migrated", actualTable), Done: i + 1, Total: len(tsTables)})
	}

	progress(Progress{Stage: "timeseries", Message: "Time-series migration complete",
		Done: len(tsTables), Total: len(tsTables)})
	return nil
}

// TSTableDDL returns the DDL and create_hypertable call for a single TS table,
// used by the SQL export feature.
func TSTableDDL(tsDB *sql.DB, srcSchema, dstSchema, table string) ([]string, error) {
	info, err := IntrospectTable(tsDB, srcSchema, table)
	if err != nil {
		return nil, err
	}
	hi, err := GetHypertableInfo(tsDB, srcSchema, info.Name)
	if err != nil {
		return nil, err
	}

	ddl := CreateTableDDL(info, dstSchema)
	createHT := fmt.Sprintf(
		`SELECT create_hypertable('%s.%s', '%s', chunk_time_interval => INTERVAL '%s', if_not_exists => TRUE);`,
		dstSchema, info.Name, hi.TimeDimension, hi.ChunkTimeInterval)

	stmts := []string{ddl, createHT}

	// Append indexes.
	stmts = append(stmts, IndexDDL(info, srcSchema, dstSchema)...)
	return stmts, nil
}

// ExportTSData generates INSERT statements for a time-series table.
func ExportTSData(tsDB *sql.DB, srcSchema, table string, tenantIDs []string) ([]string, error) {
	info, err := IntrospectTable(tsDB, srcSchema, table)
	if err != nil {
		return nil, err
	}
	colList := columnList(info.Columns)
	src := fmt.Sprintf("%s.%s", quoteIdent(srcSchema), quoteIdent(info.Name))

	var rows *sql.Rows
	if info.HasTenantID && len(tenantIDs) > 0 {
		var err error
		rows, err = tsDB.Query(
			fmt.Sprintf(`SELECT %s FROM %s WHERE tenant_id::text = ANY($1::text[]) ORDER BY 1`, colList, src),
			pq.Array(tenantIDs))
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		rows, err = tsDB.Query(fmt.Sprintf(`SELECT %s FROM %s ORDER BY 1`, colList, src))
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	return rowsToInserts(rows, info, srcSchema)
}

// rowsToInserts converts query result rows into INSERT statements.
func rowsToInserts(rows *sql.Rows, info *TableInfo, schema string) ([]string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	dst := fmt.Sprintf("%s.%s", quoteIdent(schema), quoteIdent(info.Name))
	colList := strings.Join(quotedIdents(cols), ", ")

	var stmts []string
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		valueList := make([]string, len(cols))
		for i, v := range vals {
			valueList[i] = sqlLiteral(v)
		}
		stmts = append(stmts, fmt.Sprintf(
			`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING;`,
			dst, colList, strings.Join(valueList, ", ")))
	}
	return stmts, rows.Err()
}

func quotedIdents(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return out
}

func sqlLiteral(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case []byte:
		return fmt.Sprintf("'%s'", strings.ReplaceAll(string(val), "'", "''"))
	case string:
		return fmt.Sprintf("'%s'", strings.ReplaceAll(val, "'", "''"))
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	default:
		return fmt.Sprintf("%v", v)
	}
}
