package db

import (
	"database/sql"
	"fmt"
	"strings"

	"data_factory/internal/config"
	_ "github.com/lib/pq"
)

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
		cfg.Host, cfg.Port, cfg.DBName, cfg.User, cfg.Password, sslmode, schema,
	)
}

// Open returns a *sql.DB for the given config, verifying connectivity.
func Open(cfg config.DBConfig) (*sql.DB, error) {
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
func OpenDstTS(cfg config.AppConfig) (*sql.DB, error) {
	if cfg.SameDB {
		dstCfg := cfg.SrcMain
		dstCfg.Schema = cfg.DstTS.Schema
		if dstCfg.Schema == "" {
			dstCfg.Schema = "public"
		}
		return Open(dstCfg)
	}
	tsCfg := cfg.DstTS
	if tsCfg.DBName == "" {
		tsCfg.DBName = cfg.DstMain.DBName
	}
	return Open(tsCfg)
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
