package db

import (
	"database/sql"
	"fmt"
	"strings"

	"data_factory/internal/config"
	_ "github.com/lib/pq"
)

// quoteConnStr escapes a value for libpq keyword/value connection strings.
// Values containing spaces, quotes, backslashes or empty strings must be
// single-quoted; backslash and single-quote are escaped with a backslash.
func quoteConnStr(s string) string {
	r := strings.ReplaceAll(s, `\`, `\\`)
	r = strings.ReplaceAll(r, `'`, `\'`)
	return "'" + r + "'"
}

// DSN builds a libpq connection string from a DBConfig.
func DSN(cfg config.DBConfig) string {
	sslmode := cfg.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}
	schema := cfg.Schema
	if schema == "" {
		schema = "public"
	}
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s search_path=%s",
		quoteConnStr(cfg.Host), cfg.Port, quoteConnStr(cfg.DBName), quoteConnStr(cfg.User), quoteConnStr(cfg.Password), quoteConnStr(sslmode), quoteConnStr(schema),
	)
}

// Open returns a *sql.DB for the given config, verifying connectivity.
func Open(cfg config.DBConfig) (*sql.DB, error) {
	if cfg.User == "" {
		return nil, fmt.Errorf("ping %s:%d/%s: 用户名为空，请填写 user 后再测试", cfg.Host, cfg.Port, cfg.DBName)
	}
	db, err := sql.Open("postgres", DSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s:%d/%s: %w", cfg.Host, cfg.Port, cfg.DBName, err)
	}
	return db, nil
}

// OpenSrcMain opens the source main database connection.
func OpenSrcMain(cfg config.AppConfig) (*sql.DB, error) {
	return Open(cfg.SrcMain)
}

// OpenSrcTS opens the source time-series database connection.
// It reuses the same server as SrcMain but sets search_path to the TS schema.
func OpenSrcTS(cfg config.AppConfig) (*sql.DB, error) {
	tsCfg := cfg.SrcMain
	tsCfg.Schema = cfg.SrcTS.Schema
	if tsCfg.Schema == "" {
		tsCfg.Schema = "public"
	}
	return Open(tsCfg)
}

// OpenDstMain opens the destination main database connection.
// When SameDB, it connects to the same server as SrcMain but targets DstMain's schema.
func OpenDstMain(cfg config.AppConfig) (*sql.DB, error) {
	if cfg.SameDB {
		dstCfg := cfg.SrcMain
		dstCfg.Schema = cfg.DstMain.Schema
		if dstCfg.Schema == "" {
			dstCfg.Schema = "public"
		}
		return Open(dstCfg)
	}
	return Open(cfg.DstMain)
}

// OpenDstTS opens the destination time-series database connection.
// DstTS always shares the same server/credentials as DstMain; only the schema differs.
func OpenDstTS(cfg config.AppConfig) (*sql.DB, error) {
	var baseCfg config.DBConfig
	if cfg.SameDB {
		// SameDB: DstMain is the same instance as SrcMain
		baseCfg = cfg.SrcMain
	} else {
		// Different server: use DstMain connection, substitute TS schema
		baseCfg = cfg.DstMain
	}
	baseCfg.Schema = cfg.DstTS.Schema
	if baseCfg.Schema == "" {
		baseCfg.Schema = "public"
	}
	return Open(baseCfg)
}

// SchemaOf returns the effective schema name for a DBConfig.
func SchemaOf(cfg config.DBConfig) string {
	if cfg.Schema == "" {
		return "public"
	}
	return cfg.Schema
}

// IsSameDB reports whether source and destination point at the same PostgreSQL instance.
func IsSameDB(cfg config.AppConfig) bool {
	src := cfg.SrcMain
	dst := cfg.DstMain
	return strings.EqualFold(src.Host, dst.Host) &&
		src.Port == dst.Port &&
		strings.EqualFold(src.DBName, dst.DBName)
}
