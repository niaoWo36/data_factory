package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
func GetTSTables(mainDB *sql.DB, mainSchema string, tenantIDs []string) ([]TSTableName, error) {
	src := fmt.Sprintf("%s.thing_product", quoteIdent(mainSchema))
	var q string
	var args []interface{}
	if len(tenantIDs) > 0 {
		q = fmt.Sprintf(`
			SELECT DISTINCT iot_table
			FROM %s
			WHERE iot_table IS NOT NULL AND iot_table != ''
			  AND tenant_id = ANY($1::text[])
			ORDER BY iot_table`, src)
		args = []interface{}{pqArray(tenantIDs)}
	} else {
		q = fmt.Sprintf(`
			SELECT DISTINCT iot_table
			FROM %s
			WHERE iot_table IS NOT NULL AND iot_table != ''
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
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// HypertableInfo holds the time-dimension column and chunk interval of a hypertable.
type HypertableInfo struct {
	TimeDimension     string
	ChunkTimeInterval string // interval string e.g. "7 days"
}

// GetHypertableInfo retrieves the time-dimension column and chunk interval for the given hypertable.
func GetHypertableInfo(tsDB *sql.DB, schema, table string) (*HypertableInfo, error) {
	const q = `
		SELECT d.column_name,
		       _timescaledb_functions.to_interval(d.interval_length)::text
		FROM _timescaledb_catalog.hypertable h
		JOIN _timescaledb_catalog.dimension d ON d.hypertable_id = h.id
		WHERE h.schema_name = $1 AND h.table_name = $2
		  AND d.column_type != 'integer'
		LIMIT 1`
	var hi HypertableInfo
	err := tsDB.QueryRow(q, schema, table).Scan(&hi.TimeDimension, &hi.ChunkTimeInterval)
	if err == sql.ErrNoRows {
		// Try the older API (pre-2.x)
		const q2 = `
			SELECT d.column_name,
			       _timescaledb_internal.to_interval(d.interval_length)::text
			FROM _timescaledb_catalog.hypertable h
			JOIN _timescaledb_catalog.dimension d ON d.hypertable_id = h.id
			WHERE h.schema_name = $1 AND h.table_name = $2 LIMIT 1`
		err2 := tsDB.QueryRow(q2, schema, table).Scan(&hi.TimeDimension, &hi.ChunkTimeInterval)
		if err2 != nil {
			// Default fallback
			hi.TimeDimension = "time"
			hi.ChunkTimeInterval = "7 days"
			return &hi, nil
		}
	} else if err != nil {
		return nil, fmt.Errorf("get hypertable info %s.%s: %w", schema, table, err)
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
		progress(Progress{Stage: "timeseries", Message: "No time-series tables found for selected tenants"})
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
		ddl := CreateTableDDL(info, dstTSSchema)
		if _, err := dstTSDB.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create ts table %s: %w", table, err)
		}

		// Get hypertable metadata.
		hi, err := GetHypertableInfo(srcTSDB, srcTSSchema, table)
		if err != nil {
			return err
		}

		// Convert to hypertable on destination.
		createHT := fmt.Sprintf(
			`SELECT create_hypertable('%s.%s', '%s', chunk_time_interval => INTERVAL '%s', if_not_exists => TRUE)`,
			dstTSSchema, table, hi.TimeDimension, hi.ChunkTimeInterval)
		if _, err := dstTSDB.ExecContext(ctx, createHT); err != nil {
			return fmt.Errorf("create_hypertable %s: %w", table, err)
		}

		// Migrate data.
		if sameDB {
			if err := migrateDataSameDB(ctx, srcTSDB, info, srcTSSchema, dstTSSchema, tenantIDs, progress); err != nil {
				return fmt.Errorf("migrate ts data %s: %w", table, err)
			}
		} else {
			if err := migrateDataCrossDB(ctx, srcTSDB, dstTSDB, info, srcTSSchema, dstTSSchema, tenantIDs, progress); err != nil {
				return fmt.Errorf("migrate ts data %s: %w", table, err)
			}
		}

		progress(Progress{Stage: "timeseries", Table: table,
			Message: fmt.Sprintf("TS table %s migrated", table), Done: i + 1, Total: len(tsTables)})
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
	hi, err := GetHypertableInfo(tsDB, srcSchema, table)
	if err != nil {
		return nil, err
	}

	ddl := CreateTableDDL(info, dstSchema)
	createHT := fmt.Sprintf(
		`SELECT create_hypertable('%s.%s', '%s', chunk_time_interval => INTERVAL '%s', if_not_exists => TRUE);`,
		dstSchema, table, hi.TimeDimension, hi.ChunkTimeInterval)

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
	src := fmt.Sprintf("%s.%s", quoteIdent(srcSchema), quoteIdent(table))

	var rows *sql.Rows
	if info.HasTenantID && len(tenantIDs) > 0 {
		var err error
		rows, err = tsDB.Query(
			fmt.Sprintf(`SELECT %s FROM %s WHERE tenant_id = ANY($1::text[]) ORDER BY 1`, colList, src),
			pqArray(tenantIDs))
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
