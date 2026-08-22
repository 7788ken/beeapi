package common

const (
	DatabaseTypeMySQL      = "mysql"
	DatabaseTypeSQLite     = "sqlite"
	DatabaseTypePostgreSQL = "postgres"
	DatabaseTypeClickHouse = "clickhouse"
)

var UsingSQLite = false
var UsingPostgreSQL = false
var LogSqlType = DatabaseTypeSQLite // Default to SQLite for logging SQL queries
var UsingMySQL = false

func UsingMainDatabase(databaseType string) bool {
	switch databaseType {
	case DatabaseTypeMySQL:
		return UsingMySQL
	case DatabaseTypePostgreSQL:
		return UsingPostgreSQL
	case DatabaseTypeSQLite:
		return UsingSQLite
	default:
		return false
	}
}

func UsingLogDatabase(databaseType string) bool {
	return LogSqlType == databaseType
}

var SQLitePath = "one-api.db?_busy_timeout=30000"
