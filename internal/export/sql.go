package export

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"data_factory/internal/config"
	"data_factory/internal/db"
)

// GenerateSQL generates a SQL migration script and writes it to a string.
// Options:
//   - includeMain  – include main-database DDL (and optionally DML)
//   - includeTS    – include time-series DDL (and optionally DML)
//   - includeData  – include INSERT statements for tenant data
func GenerateSQL(
	srcMainDB, dstMainDB *sql.DB,
	srcTSDB *sql.DB,
	mainDB *sql.DB,
	cfg config.AppConfig,
	tenantIDs []string,
	includeMain, includeTS, includeData bool,
) (string, error) {
	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("-- =============================================================\n"))
	sb.WriteString(fmt.Sprintf("-- data_factory SQL export\n"))
	sb.WriteString(fmt.Sprintf("-- Generated : %s\n", time.Now().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("-- Source    : %s:%d/%s schema=%s\n",
		cfg.SrcMain.Host, cfg.SrcMain.Port, cfg.SrcMain.DBName, db.SchemaOf(cfg.SrcMain)))
	if len(tenantIDs) > 0 {
		sb.WriteString(fmt.Sprintf("-- Tenants   : %s\n", strings.Join(tenantIDs, ", ")))
	} else {
		sb.WriteString("-- Tenants   : ALL\n")
	}
	sb.WriteString("-- =============================================================\n\n")

	srcSchema := db.SchemaOf(cfg.SrcMain)
	dstSchema := db.SchemaOf(cfg.DstMain)
	srcTSSchema := db.SchemaOf(cfg.SrcTS)
	dstTSSchema := db.SchemaOf(cfg.DstTS)
	var failedTables []string

	if includeMain {
		if err := writeMainDDL(&sb, srcMainDB, srcSchema, dstSchema, &failedTables); err != nil {
			return "", err
		}
		if includeData {
			if err := writeMainData(&sb, srcMainDB, srcSchema, dstSchema, tenantIDs, &failedTables); err != nil {
				return "", err
			}
		}
		seqStmts, seqFailed := sequenceSetvalStmts(srcMainDB, srcSchema, dstSchema)
		if len(seqStmts) > 0 {
			sb.WriteString("-- Sequence values\n")
			for _, s := range seqStmts {
				sb.WriteString(s)
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
		if len(seqFailed) > 0 {
			for _, f := range seqFailed {
				sb.WriteString(fmt.Sprintf("-- WARNING: %s\n", f))
			}
			failedTables = append(failedTables, seqFailed...)
		}
	}

	if includeTS {
		tsTables, err := db.GetTSTables(mainDB, srcSchema, tenantIDs)
		if err != nil {
			return "", fmt.Errorf("get ts tables: %w", err)
		}
		if len(tsTables) > 0 {
			if err := writeTSDDL(&sb, srcTSDB, srcTSSchema, dstTSSchema, tsTables); err != nil {
				return "", err
			}
			if includeData {
				sb.WriteString("-- Time-series data export skipped: TS migration/export only includes table structures.\n\n")
			}
		} else {
			sb.WriteString("-- No time-series tables found for selected tenants.\n")
		}
	}

	if len(failedTables) > 0 {
		sb.WriteString(fmt.Sprintf("-- WARNING: %d table(s) skipped during export: %s\n\n",
			len(failedTables), strings.Join(failedTables, "; ")))
	}

	return sb.String(), nil
}

func writeMainDDL(sb *strings.Builder, srcDB *sql.DB, srcSchema, dstSchema string, failedTables *[]string) error {
	sb.WriteString("-- ---------------------------------------------------------------\n")
	sb.WriteString("-- MAIN DATABASE – TABLE STRUCTURES\n")
	sb.WriteString("-- ---------------------------------------------------------------\n\n")
	sb.WriteString(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;\n\n", quoteIdent(dstSchema)))

	// Extensions (e.g. pgvector). Errors are non-fatal at restore time.
	extRows, err := srcDB.Query(`SELECT extname FROM pg_extension WHERE extname <> 'plpgsql' ORDER BY extname`)
	if err == nil {
		defer extRows.Close()
		var exts []string
		for extRows.Next() {
			var n string
			if extRows.Scan(&n) == nil {
				exts = append(exts, n)
			}
		}
		if len(exts) > 0 {
			sb.WriteString("-- Extensions\n")
			for _, e := range exts {
				sb.WriteString(fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s CASCADE;\n", quoteIdent(e)))
			}
			sb.WriteString("\n")
		}
	}

	// Sequences (must come before tables that reference them in DEFAULT clauses).
	seqs, err := db.ListSequences(srcDB, srcSchema)
	if err != nil {
		return fmt.Errorf("list sequences: %w", err)
	}
	if len(seqs) > 0 {
		sb.WriteString("-- Sequences\n")
		for _, seq := range seqs {
			sb.WriteString(db.CreateSequenceDDL(seq, dstSchema))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	tables, err := db.ListTables(srcDB, srcSchema)
	if err != nil {
		return err
	}

	var allInfos []*db.TableInfo
	for _, t := range tables {
		info, err := db.IntrospectTable(srcDB, srcSchema, t)
		if err != nil {
			sb.WriteString(fmt.Sprintf("-- WARNING: skipped DDL for %s (introspect failed): %v\n\n", quoteIdent(t), err))
			*failedTables = append(*failedTables, fmt.Sprintf("%s(introspect: %v)", t, err))
			continue
		}
		allInfos = append(allInfos, info)
		ddl := db.CreateTableDDL(info, dstSchema)
		sb.WriteString(ddl)
		sb.WriteString("\n\n")
	}

	// Indexes
	sb.WriteString("-- Indexes\n")
	for _, info := range allInfos {
		for _, stmt := range db.IndexDDL(info, srcSchema, dstSchema) {
			sb.WriteString(stmt)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")

	// Foreign keys
	sb.WriteString("-- Foreign keys\n")
	for _, info := range allInfos {
		for _, stmt := range db.ForeignKeyDDL(info, srcSchema, dstSchema) {
			sb.WriteString(stmt)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n")

	return nil
}

func writeMainData(sb *strings.Builder, srcDB *sql.DB, srcSchema, dstSchema string, tenantIDs []string, failedTables *[]string) error {
	sb.WriteString("-- ---------------------------------------------------------------\n")
	sb.WriteString("-- MAIN DATABASE – DATA\n")
	sb.WriteString("-- ---------------------------------------------------------------\n\n")

	tables, err := db.ListTables(srcDB, srcSchema)
	if err != nil {
		return err
	}
	for _, table := range tables {
		// Schema-only tables: skip data in export too.
		if db.SchemaOnlyTables[table] {
			sb.WriteString(fmt.Sprintf("-- Skipped data for %s (schema-only table)\n", table))
			continue
		}

		info, err := db.IntrospectTable(srcDB, srcSchema, table)
		if err != nil {
			sb.WriteString(fmt.Sprintf("-- WARNING: skipped data for %s (introspect failed): %v\n", quoteIdent(table), err))
			*failedTables = append(*failedTables, fmt.Sprintf("%s(introspect: %v)", table, err))
			continue
		}
		colList := columnNames(info.Columns)
		src := fmt.Sprintf("%s.%s", quoteIdent(srcSchema), quoteIdent(table))
		dst := fmt.Sprintf("%s.%s", quoteIdent(dstSchema), quoteIdent(table))

		// For base-tenant tables always include tenant 000000.
		effectiveTenants := db.EffectiveTenantIDs(table, tenantIDs)

		var rows *sql.Rows
		if info.HasTenantID && len(effectiveTenants) > 0 {
			rows, err = srcDB.Query(
				fmt.Sprintf(`SELECT %s FROM %s WHERE %s`, colList, src, db.TenantFilterClause),
				db.TenantFilterArgs(table, tenantIDs))
		} else {
			rows, err = srcDB.Query(fmt.Sprintf(`SELECT %s FROM %s`, colList, src))
		}
		if err != nil {
			sb.WriteString(fmt.Sprintf("-- WARNING: skipped data for %s (select failed): %v\n", quoteIdent(table), err))
			*failedTables = append(*failedTables, fmt.Sprintf("%s(select: %v)", table, err))
			continue
		}

		if err := writeInserts(sb, rows, info, dst); err != nil {
			rows.Close()
			sb.WriteString(fmt.Sprintf("-- WARNING: skipped data for %s (export failed): %v\n", quoteIdent(table), err))
			*failedTables = append(*failedTables, fmt.Sprintf("%s(export: %v)", table, err))
			continue
		}
		rows.Close()
	}
	return nil
}

func writeTSDDL(sb *strings.Builder, tsDB *sql.DB, srcTSSchema, dstTSSchema string, tables []string) error {
	sb.WriteString("-- ---------------------------------------------------------------\n")
	sb.WriteString("-- TIME-SERIES DATABASE – TABLE STRUCTURES\n")
	sb.WriteString("-- ---------------------------------------------------------------\n\n")
	sb.WriteString("CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;\n")
	sb.WriteString(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;\n\n", quoteIdent(dstTSSchema)))

	for _, table := range tables {
		stmts, err := db.TSTableDDL(tsDB, srcTSSchema, dstTSSchema, table)
		if err != nil {
			sb.WriteString(fmt.Sprintf("-- WARNING: could not introspect %s: %v\n", table, err))
			continue
		}
		for _, s := range stmts {
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return nil
}

func writeTSData(sb *strings.Builder, tsDB *sql.DB, srcTSSchema string, tables []string, tenantIDs []string) error {
	sb.WriteString("-- ---------------------------------------------------------------\n")
	sb.WriteString("-- TIME-SERIES DATABASE – DATA\n")
	sb.WriteString("-- ---------------------------------------------------------------\n\n")

	for _, table := range tables {
		stmts, err := db.ExportTSData(tsDB, srcTSSchema, table, tenantIDs)
		if err != nil {
			sb.WriteString(fmt.Sprintf("-- WARNING: export data for %s failed: %v\n", table, err))
			continue
		}
		for _, s := range stmts {
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return nil
}

// writeInserts converts rows from a query into INSERT statements.
func writeInserts(sb *strings.Builder, rows *sql.Rows, info *db.TableInfo, dst string) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	colList := strings.Join(quotedIdents(cols), ", ")
	colTypes := columnTypesByName(info.Columns)

	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		valueList := make([]string, len(cols))
		for i, v := range vals {
			valueList[i] = db.SQLLiteral(v, colTypes[cols[i]])
		}
		sb.WriteString(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING;\n",
			dst, colList, strings.Join(valueList, ", ")))
	}
	return rows.Err()
}

// --- small helpers ---

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func columnNames(cols []db.Column) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quoteIdent(c.Name)
	}
	return strings.Join(names, ", ")
}

func quotedIdents(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return out
}

func columnTypesByName(cols []db.Column) map[string]string {
	m := make(map[string]string, len(cols))
	for _, c := range cols {
		m[c.Name] = c.DataType
	}
	return m
}

// sequenceSetvalStmts emits SELECT setval(...) statements so that sequence
// last_value/is_called survive an export/restore cycle. Without them the
// restored sequences restart at their definition START and new inserts collide
// with migrated keys (silently swallowed by ON CONFLICT DO NOTHING).
func sequenceSetvalStmts(srcDB *sql.DB, srcSchema, dstSchema string) ([]string, []string) {
	seqs, err := db.ListSequences(srcDB, srcSchema)
	if err != nil {
		return nil, []string{fmt.Sprintf("sequences(list: %v)", err)}
	}
	var stmts []string
	var failed []string
	for _, seq := range seqs {
		var lastVal int64
		var isCalled bool
		q := fmt.Sprintf(`SELECT last_value, is_called FROM %s.%s`, quoteIdent(srcSchema), quoteIdent(seq.Name))
		if err := srcDB.QueryRow(q).Scan(&lastVal, &isCalled); err != nil {
			failed = append(failed, fmt.Sprintf("%s(read: %v)", seq.Name, err))
			continue
		}
		stmts = append(stmts, fmt.Sprintf(`SELECT setval('%s.%s', %d, %v);`,
			strings.ReplaceAll(dstSchema, `'`, `''`), strings.ReplaceAll(seq.Name, `'`, `''`),
			lastVal, isCalled))
	}
	return stmts, failed
}
