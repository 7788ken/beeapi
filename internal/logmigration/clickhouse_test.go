package logmigration

import (
	"strings"
	"testing"
)

func TestNormalizeClickHouseDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "https adds secure",
			dsn:  "https://clickhouse.example:8443/logs?dial_timeout=10s",
			want: "https://clickhouse.example:8443/logs?dial_timeout=10s&secure=true",
		},
		{
			name: "explicit secure remains",
			dsn:  "https://clickhouse.example/logs?secure=false",
			want: "https://clickhouse.example/logs?secure=false",
		},
		{
			name: "native unchanged",
			dsn:  "clickhouse://clickhouse.example:9000/logs",
			want: "clickhouse://clickhouse.example:9000/logs",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeClickHouseDSN(test.dsn); got != test.want {
				t.Fatalf("NormalizeClickHouseDSN() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClickHouseLogCreateTableSQL(t *testing.T) {
	sql := ClickHouseLogCreateTableSQL(30)
	required := []string{
		"ENGINE = MergeTree()",
		"PARTITION BY toYYYYMM(toDateTime(created_at))",
		"ORDER BY (created_at, event_id)",
		"TTL toDateTime(created_at) + INTERVAL 30 DAY DELETE",
		"request_id String",
		"event_id String DEFAULT toString(generateUUIDv4())",
	}
	for _, fragment := range required {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("schema SQL missing %q:\n%s", fragment, sql)
		}
	}
	if sqlWithoutTTL := ClickHouseLogCreateTableSQL(0); strings.Contains(sqlWithoutTTL, "\nTTL ") {
		t.Fatalf("zero TTL unexpectedly emitted TTL clause:\n%s", sqlWithoutTTL)
	}
}

func TestClickHouseCreateTableTTLMatchesCanonicalForms(t *testing.T) {
	for _, createTableSQL := range []string{
		"CREATE TABLE logs (...) TTL toDateTime(created_at) + INTERVAL 30 DAY DELETE",
		"CREATE TABLE logs (...) TTL toDateTime(created_at) + INTERVAL 30 DAY",
		"CREATE TABLE logs (...) TTL toDateTime(created_at) + toIntervalDay(30) DELETE",
		"CREATE TABLE logs (...) TTL toDateTime(created_at) + toIntervalDay(30)",
	} {
		if !clickHouseCreateTableTTLMatches(createTableSQL, 30) {
			t.Fatalf("canonical ClickHouse TTL was rejected: %s", createTableSQL)
		}
	}
	if clickHouseCreateTableTTLMatches(
		"CREATE TABLE logs (...) TTL toDateTime(created_at) + toIntervalDay(31)",
		30,
	) {
		t.Fatal("mismatched ClickHouse TTL was accepted")
	}
	if clickHouseCreateTableTTLMatches(
		"CREATE TABLE logs (...) TTL toDateTime(created_at) + toIntervalDay(30) TO DISK 'cold'",
		30,
	) {
		t.Fatal("non-delete ClickHouse TTL action was accepted")
	}
}

func TestClickHouseDSNRecognition(t *testing.T) {
	for _, dsn := range []string{
		"clickhouse://localhost/logs",
		"tcp://localhost/logs",
		"http://localhost/logs",
		"https://localhost/logs",
	} {
		if !IsClickHouseDSN(dsn) {
			t.Fatalf("expected ClickHouse DSN: %s", dsn)
		}
	}
	if IsClickHouseDSN("postgres://localhost/logs") {
		t.Fatal("PostgreSQL DSN recognized as ClickHouse")
	}
}
