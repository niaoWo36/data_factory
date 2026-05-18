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

	if includeMain {
		if err := writeMainDDL(&sb, srcMainDB, srcSchema, dstSchema); err != nil {
			return "", err
		}
		if includeData {
			if err := writeMainData(&sb, srcMainDB, srcSchema, dstSchema, tenantIDs); err != nil {
				return "", err
			}
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
				if err := writeTSData(&sb, srcTSDB, srcTSSchema, tsTables, tenantIDs); err != nil {
					return "", err
				}
			}
		} else {
			sb.WriteString("-- No time-series tables found for selected tenants.\n")
		}
	}

	return sb.String(), nil
}

func writeMainDDL(sb *strings.Builder, srcDB *sql.DB, srcSchema, dstSchema string) error {
	sb.WriteString("-- ---------------------------------------------------------------\n")
	sb.WriteString("-- MAIN DATABASE – TABLE STRUCTURES\n")
	sb.WriteString("-- ---------------------------------------------------------------\n\n")
	sb.WriteString(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;\n\n", quoteIdent(dstSchema)))

	tables, err := db.ListTables(srcDB, srcSchema)
	if err != nil {
		return err
	}

	var allInfos []*db.TableInfo
	for _, t := range tables {
		info, err := db.IntrospectTable(srcDB, srcSchema, t)
		if err != nil {
			return fmt.Errorf("introspect %s: %w", t, err)
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

func writeMainData(sb *strings.Builder, srcDB *sql.DB, srcSchema, dstSchema string, tenantIDs []string) error {
	sb.WriteString("-- ---------------------------------------------------------------\n")
	sb.WriteString("-- MAIN DATABASE – DATA\n")
	sb.WriteString("-- ---------------------------------------------------------------\n\n")

	tables, err := db.ListTables(srcDB, srcSchema)
	if err != nil {
		return err
	}
	for _, table := range tables {
		info, err := db.IntrospectTable(srcDB, srcSchema, table)
		if err != nil {
			return err
		}
		colList := columnNames(info.Columns)
		src := fmt.Sprintf("%s.%s", quoteIdent(srcSchema), quoteIdent(table))
		dst := fmt.Sprintf("%s.%s", quoteIdent(dstSchema), quoteIdent(table))

		var rows *sql.Rows
		if info.HasTenantID && len(tenantIDs) > 0 {
			rows, err = srcDB.Query(
				fmt.Sprintf(`SELECT %s FROM %s WHERE tenant_id = ANY($1::text[]) ORDER BY 1`, colList, src),
				pqArray(tenantIDs))
		} else {
			rows, err = srcDB.Query(fmt.Sprintf(`SELECT %s FROM %s ORDER BY 1`, colList, src))
		}
		if err != nil {
			return fmt.Errorf("select %s: %w", table, err)
		}

		if err := writeInserts(sb, rows, info, dst); err != nil {
			rows.Close()
			return err
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
			valueList[i] = sqlLiteral(v)
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

func pqArray(ss []string) interface{} {
	escaped := make([]string, len(ss))
	for i, s := range ss {
		escaped[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "{" + strings.Join(escaped, ",") + "}"
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
