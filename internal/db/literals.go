package db

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// SQLLiteral converts a scanned driver value into a PostgreSQL literal for use
// in generated INSERT statements. colType is the introspected column type
// (e.g. "bytea", "text[]") and is used to disambiguate []byte values.
func SQLLiteral(v interface{}, colType string) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return strconv.Itoa(val)
	case int8:
		return strconv.FormatInt(int64(val), 10)
	case int16:
		return strconv.FormatInt(int64(val), 10)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	case uint8:
		return strconv.FormatUint(uint64(val), 10)
	case uint16:
		return strconv.FormatUint(uint64(val), 10)
	case uint32:
		return strconv.FormatUint(uint64(val), 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	case float32:
		return floatLiteral(float64(val))
	case float64:
		return floatLiteral(val)
	case string:
		return quoteStringLiteral(val)
	case []byte:
		if strings.Contains(colType, "bytea") || !utf8.Valid(val) {
			return fmt.Sprintf(`'\x%x'`, val)
		}
		return quoteStringLiteral(string(val))
	case time.Time:
		if val.IsZero() {
			return "NULL"
		}
		return quoteStringLiteral(val.Format("2006-01-02 15:04:05.999999999Z07:00"))
	default:
		return quoteStringLiteral(fmt.Sprintf("%v", v))
	}
}

func floatLiteral(f float64) string {
	switch {
	case math.IsNaN(f):
		return "'NaN'"
	case math.IsInf(f, 1):
		return "'Infinity'"
	case math.IsInf(f, -1):
		return "'-Infinity'"
	default:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
}

func quoteStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
