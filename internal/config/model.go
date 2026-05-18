package config

// DBConfig holds connection parameters for a single database endpoint.
type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DBName   string `json:"dbname"`
	User     string `json:"user"`
	Password string `json:"password"`
	Schema   string `json:"schema"` // defaults to "public"
	SSLMode  string `json:"sslmode"` // disable | require | verify-full
}

// AppConfig groups all four connection configurations used by the tool.
type AppConfig struct {
	// SrcMain is the source main (relational) database.
	SrcMain DBConfig `json:"src_main"`
	// SrcTS is the source time-series database; it shares the same host/port/dbname
	// as SrcMain – only the schema differs.
	SrcTS DBConfig `json:"src_ts"`

	// DstMain is the target main database.
	// When SameDB is true, only DBName and Schema are used.
	DstMain DBConfig `json:"dst_main"`
	// DstTS is the target time-series database.
	// When SameDB is true, only Schema is used (DBName taken from DstMain).
	DstTS DBConfig `json:"dst_ts"`

	// SameDB indicates that source and destination share the same server instance.
	// Detected automatically by comparing host/port/dbname of SrcMain and DstMain.
	SameDB bool `json:"same_db"`
}

// MigrateOptions carries per-request migration flags sent from the UI.
type MigrateOptions struct {
	Config     AppConfig `json:"config"`
	TenantIDs  []string  `json:"tenant_ids"`
	MigrateSchema     bool `json:"migrate_schema"`
	MigrateData       bool `json:"migrate_data"`
	MigrateTimeSeries bool `json:"migrate_time_series"`
}

// ExportOptions carries per-request SQL export flags sent from the UI.
type ExportOptions struct {
	Config        AppConfig `json:"config"`
	TenantIDs     []string  `json:"tenant_ids"`
	IncludeMain   bool      `json:"include_main"`
	IncludeTS     bool      `json:"include_ts"`
	IncludeData   bool      `json:"include_data"` // false = DDL only
}
