package db

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Sequence holds introspected metadata for a PostgreSQL sequence.
type Sequence struct {
	Name      string
	DataType  string
	Start     int64
	Increment int64
	Min       int64
	Max       int64
	Cycle     bool
}

// ListSequences returns all sequences defined in the given schema.
func ListSequences(conn *sql.DB, schema string) ([]Sequence, error) {
	rows, err := conn.Query(`
		SELECT sequence_name, data_type,
		       start_value::bigint, increment::bigint,
		       minimum_value::bigint, maximum_value::bigint,
		       cycle_option
		FROM information_schema.sequences
		WHERE sequence_schema = $1
		ORDER BY sequence_name`, schema)
	if err != nil {
		return nil, fmt.Errorf("list sequences: %w", err)
	}
	defer rows.Close()
	var seqs []Sequence
	for rows.Next() {
		var s Sequence
		var cycle string
		if err := rows.Scan(&s.Name, &s.DataType, &s.Start, &s.Increment, &s.Min, &s.Max, &cycle); err != nil {
			return nil, err
		}
		s.Cycle = cycle == "YES"
		seqs = append(seqs, s)
	}
	return seqs, rows.Err()
}

// CreateSequenceDDL generates a CREATE SEQUENCE IF NOT EXISTS statement for the
// given sequence, placing it in dstSchema.
func CreateSequenceDDL(seq Sequence, dstSchema string) string {
	cycle := "NO CYCLE"
	if seq.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf(
		`CREATE SEQUENCE IF NOT EXISTS %s.%s AS %s START %d INCREMENT %d MINVALUE %d MAXVALUE %d %s;`,
		quoteIdent(dstSchema), quoteIdent(seq.Name),
		seq.DataType,
		seq.Start, seq.Increment, seq.Min, seq.Max, cycle,
	)
}

// nextvalRe matches nextval('...') expressions in column defaults.
var nextvalRe = regexp.MustCompile(`nextval\('([^']+)'::regclass\)`)

// rewriteNextval rewrites nextval(...) expressions so they reference the sequence
// inside dstSchema rather than whatever schema was used in the source.
func rewriteNextval(val, dstSchema string) string {
	return nextvalRe.ReplaceAllStringFunc(val, func(match string) string {
		sub := nextvalRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		// Extract the bare sequence name – strip any existing schema prefix and quotes.
		seqRef := sub[1]
		parts := strings.Split(seqRef, ".")
		seqName := strings.Trim(parts[len(parts)-1], `"`)
		return fmt.Sprintf("nextval('%s.%s'::regclass)", quoteIdent(dstSchema), quoteIdent(seqName))
	})
}


type Column struct {
	Name       string
	DataType   string
	IsNullable bool
	Default    sql.NullString
	// OrdinalPos is used to preserve column order.
	OrdinalPos int
}

// Constraint represents a table-level constraint (PK, UNIQUE, CHECK, FK).
type Constraint struct {
	Name       string
	Type       string // PRIMARY KEY | UNIQUE | CHECK | FOREIGN KEY
	Definition string // full constraint definition clause
}

// Index represents a non-primary, non-unique-constraint index.
type Index struct {
	Name       string
	Definition string // full CREATE INDEX statement
}

// TableInfo holds all introspected information for one table.
type TableInfo struct {
	Schema      string
	Name        string
	Columns     []Column
	Constraints []Constraint
	Indexes     []Index
	HasTenantID bool // true when a column named tenant_id exists
}

// ListTables returns the ordered list of user-defined table names in the given schema.
func ListTables(db *sql.DB, schema string) ([]string, error) {
	const q = `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = $1
		  AND table_type = 'BASE TABLE'
		ORDER BY table_name`
	rows, err := db.Query(q, schema)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// IntrospectTable returns full metadata for the named table in the given schema.
func IntrospectTable(db *sql.DB, schema, table string) (*TableInfo, error) {
	info := &TableInfo{Schema: schema, Name: table}

	// --- Columns ---
	colRows, err := db.Query(`
		SELECT column_name, data_type, is_nullable,
		       column_default, ordinal_position,
		       character_maximum_length, numeric_precision, numeric_scale,
		       udt_name
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	defer colRows.Close()
	for colRows.Next() {
		var c Column
		var nullable string
		var charLen, numPrec, numScale sql.NullInt64
		var udtName string
		if err := colRows.Scan(&c.Name, &c.DataType, &nullable, &c.Default, &c.OrdinalPos,
			&charLen, &numPrec, &numScale, &udtName); err != nil {
			return nil, err
		}
		c.IsNullable = nullable == "YES"
		// Resolve full type name
		c.DataType = resolveType(c.DataType, udtName, charLen, numPrec, numScale)
		if strings.EqualFold(c.Name, "tenant_id") {
			info.HasTenantID = true
		}
		info.Columns = append(info.Columns, c)
	}
	if err := colRows.Err(); err != nil {
		return nil, err
	}

	// --- Constraints ---
	conRows, err := db.Query(`
		SELECT con.conname, con.contype,
		       pg_get_constraintdef(con.oid, true) as def
		FROM pg_constraint con
		JOIN pg_class cls ON cls.oid = con.conrelid
		JOIN pg_namespace ns  ON ns.oid  = cls.relnamespace
		WHERE ns.nspname = $1 AND cls.relname = $2
		ORDER BY con.contype, con.conname`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("constraints: %w", err)
	}
	defer conRows.Close()
	for conRows.Next() {
		var cn Constraint
		var ctype string
		if err := conRows.Scan(&cn.Name, &ctype, &cn.Definition); err != nil {
			return nil, err
		}
		switch ctype {
		case "p":
			cn.Type = "PRIMARY KEY"
		case "u":
			cn.Type = "UNIQUE"
		case "c":
			cn.Type = "CHECK"
		case "f":
			cn.Type = "FOREIGN KEY"
		default:
			cn.Type = ctype
		}
		info.Constraints = append(info.Constraints, cn)
	}
	if err := conRows.Err(); err != nil {
		return nil, err
	}

	// --- Indexes (non-constraint) ---
	idxRows, err := db.Query(`
		SELECT indexname, indexdef
		FROM pg_indexes
		WHERE schemaname = $1 AND tablename = $2
		  AND indexname NOT IN (
		      SELECT conname FROM pg_constraint
		      WHERE conrelid = (
		          SELECT oid FROM pg_class
		          WHERE relname = $2
		            AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = $1)
		      )
		  )
		ORDER BY indexname`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("indexes: %w", err)
	}
	defer idxRows.Close()
	for idxRows.Next() {
		var idx Index
		if err := idxRows.Scan(&idx.Name, &idx.Definition); err != nil {
			return nil, err
		}
		info.Indexes = append(info.Indexes, idx)
	}
	return info, idxRows.Err()
}

// CreateTableDDL generates a CREATE TABLE IF NOT EXISTS statement for the given
// TableInfo in the target schema. Foreign-key constraints are omitted so that
// tables can be created in any order; call ForeignKeyDDL separately.
func CreateTableDDL(info *TableInfo, targetSchema string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s (\n",
		quoteIdent(targetSchema), quoteIdent(info.Name)))

	// Columns
	for i, c := range info.Columns {
		sb.WriteString(fmt.Sprintf("    %s %s", quoteIdent(c.Name), c.DataType))
		if !c.IsNullable {
			sb.WriteString(" NOT NULL")
		}
		if c.Default.Valid {
				def := rewriteNextval(c.Default.String, targetSchema)
				sb.WriteString(fmt.Sprintf(" DEFAULT %s", def))
			}
		if i < len(info.Columns)-1 || hasNonFKConstraints(info) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}

	// Non-FK constraints inline
	nonFK := filterConstraints(info.Constraints, func(cn Constraint) bool {
		return cn.Type != "FOREIGN KEY"
	})
	for i, cn := range nonFK {
		sb.WriteString(fmt.Sprintf("    CONSTRAINT %s %s",
			quoteIdent(cn.Name), cn.Definition))
		if i < len(nonFK)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(");")
	return sb.String()
}

// ForeignKeyDDL generates ALTER TABLE … ADD CONSTRAINT … statements for all FK
// constraints of the table, replacing the source schema with targetSchema.
func ForeignKeyDDL(info *TableInfo, srcSchema, targetSchema string) []string {
	var stmts []string
	for _, cn := range info.Constraints {
		if cn.Type != "FOREIGN KEY" {
			continue
		}
		// Replace schema references in the definition.
		def := strings.ReplaceAll(cn.Definition,
			quoteIdent(srcSchema)+".", quoteIdent(targetSchema)+".")
		// Also handle unquoted schema prefix.
		def = strings.ReplaceAll(def, srcSchema+".", targetSchema+".")
		stmts = append(stmts, fmt.Sprintf(
			"ALTER TABLE %s.%s ADD CONSTRAINT %s %s;",
			quoteIdent(targetSchema), quoteIdent(info.Name),
			quoteIdent(cn.Name), def,
		))
	}
	return stmts
}

// IndexDDL generates CREATE INDEX statements replacing the source schema with targetSchema.
func IndexDDL(info *TableInfo, srcSchema, targetSchema string) []string {
	var stmts []string
	for _, idx := range info.Indexes {
		def := strings.ReplaceAll(idx.Definition,
			" ON "+quoteIdent(srcSchema)+"."+quoteIdent(info.Name),
			" ON "+quoteIdent(targetSchema)+"."+quoteIdent(info.Name))
		def = strings.ReplaceAll(def,
			" ON "+srcSchema+"."+info.Name,
			" ON "+quoteIdent(targetSchema)+"."+quoteIdent(info.Name))
		// Add IF NOT EXISTS
		def = strings.Replace(def, "CREATE INDEX ", "CREATE INDEX IF NOT EXISTS ", 1)
		def = strings.Replace(def, "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX IF NOT EXISTS ", 1)
		stmts = append(stmts, def+";")
	}
	return stmts
}

// --- helpers ---

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func resolveType(dataType, udtName string, charLen, numPrec, numScale sql.NullInt64) string {
	switch dataType {
	case "character varying":
		if charLen.Valid {
			return fmt.Sprintf("varchar(%d)", charLen.Int64)
		}
		return "varchar"
	case "character":
		if charLen.Valid {
			return fmt.Sprintf("char(%d)", charLen.Int64)
		}
		return "char"
	case "numeric":
		if numPrec.Valid && numScale.Valid {
			return fmt.Sprintf("numeric(%d,%d)", numPrec.Int64, numScale.Int64)
		}
		return "numeric"
	case "ARRAY":
		return udtName // e.g. "_text" → keep as-is; caller can refine
	case "USER-DEFINED":
		return udtName
	default:
		return dataType
	}
}

func hasNonFKConstraints(info *TableInfo) bool {
	for _, cn := range info.Constraints {
		if cn.Type != "FOREIGN KEY" {
			return true
		}
	}
	return false
}

func filterConstraints(cs []Constraint, fn func(Constraint) bool) []Constraint {
	var out []Constraint
	for _, c := range cs {
		if fn(c) {
			out = append(out, c)
		}
	}
	return out
}
