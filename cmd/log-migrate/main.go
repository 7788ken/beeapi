package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/QuantumNous/new-api/internal/logmigration"
)

func fatalf(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "log-migrate: "+format+"\n", values...)
	os.Exit(1)
}

func defaultTTLDays() int {
	raw := os.Getenv("LOG_SQL_CLICKHOUSE_TTL_DAYS")
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		fatalf("LOG_SQL_CLICKHOUSE_TTL_DAYS must be a non-negative integer")
	}
	return value
}

func main() {
	var (
		statePath = flag.String("state", "log-migration-state.json", "resume state file")
		batchSize = flag.Int("batch-size", 1000, "rows per insert batch")
		ttlDays   = flag.Int("ttl-days", defaultTTLDays(), "ClickHouse TTL days; defaults to LOG_SQL_CLICKHOUSE_TTL_DAYS, 0 disables TTL")
		initOnly  = flag.Bool("init-schema-only", false, "create or validate the ClickHouse schema and TTL, then exit")
		dryRun    = flag.Bool("dry-run", false, "validate connections and print captured high-water without writing")
		verify    = flag.Bool("verify-only", false, "only reconcile the completed state window")
		advance   = flag.Bool("advance", false, "capture a new high-water after a completed window")
	)
	flag.Parse()

	sourceDSN := os.Getenv("LOG_MIGRATION_SOURCE_DSN")
	if sourceDSN == "" {
		sourceDSN = os.Getenv("SQL_DSN")
	}
	targetDSN := os.Getenv("LOG_SQL_DSN")
	if targetDSN == "" {
		fatalf("LOG_SQL_DSN is required")
	}
	if !logmigration.IsClickHouseDSN(targetDSN) {
		fatalf("LOG_SQL_DSN must be ClickHouse")
	}
	if *batchSize <= 0 {
		fatalf("batch-size must be positive")
	}
	if *ttlDays < 0 {
		fatalf("ttl-days cannot be negative")
	}
	if *verify && *advance {
		fatalf("verify-only and advance cannot be used together")
	}
	if *initOnly && (*dryRun || *verify || *advance) {
		fatalf("init-schema-only cannot be combined with dry-run, verify-only, or advance")
	}
	if needsStateLock(*initOnly, *dryRun) {
		releaseLock, err := logmigration.AcquireStateLock(*statePath)
		if err != nil {
			fatalf("acquire state lock: %v", err)
		}
		defer func() {
			if err := releaseLock(); err != nil {
				fmt.Fprintf(os.Stderr, "log-migrate: release state lock: %v\n", err)
			}
		}()
	}

	sqlitePath := os.Getenv("LOG_MIGRATION_SOURCE_SQLITE_PATH")
	if sqlitePath == "" {
		sqlitePath = os.Getenv("SQLITE_PATH")
	}
	target, err := logmigration.OpenClickHouse(targetDSN)
	if err != nil {
		fatalf("open target: %v", err)
	}
	targetSQL, err := target.DB()
	if err != nil {
		fatalf("target sql handle: %v", err)
	}
	defer targetSQL.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := targetSQL.PingContext(ctx); err != nil {
		fatalf("target ping: %v", err)
	}

	if *initOnly {
		if err := logmigration.EnsureClickHouseLogSchema(ctx, target, *ttlDays); err != nil {
			fatalf("ensure target schema: %v", err)
		}
		output, _ := json.MarshalIndent(map[string]any{
			"schema":   "logs",
			"ttl_days": *ttlDays,
			"verified": true,
		}, "", "  ")
		fmt.Println(string(output))
		return
	}

	source, err := logmigration.OpenSource(sourceDSN, sqlitePath)
	if err != nil {
		fatalf("open source: %v", err)
	}
	sourceSQL, err := source.DB()
	if err != nil {
		fatalf("source sql handle: %v", err)
	}
	defer sourceSQL.Close()
	if err := sourceSQL.PingContext(ctx); err != nil {
		fatalf("source ping: %v", err)
	}

	fingerprint := logmigration.ConnectionFingerprint(sourceDSN, sqlitePath, targetDSN)
	state, err := logmigration.LoadState(*statePath)
	if err != nil {
		fatalf("load state: %v", err)
	}
	if state.Version != 0 && state.ConnectionFingerprint != fingerprint {
		fatalf("state belongs to different source/target connections")
	}

	if *dryRun {
		if err := logmigration.ValidateClickHouseLogSchema(ctx, target); err != nil {
			fatalf("validate target schema: %v", err)
		}
		if err := logmigration.ValidateClickHouseLogTTL(ctx, target, *ttlDays); err != nil {
			fatalf("validate target TTL: %v", err)
		}
		highWater, err := logmigration.CaptureHighWater(ctx, source)
		if err != nil {
			fatalf("capture high-water: %v", err)
		}
		output, _ := json.MarshalIndent(map[string]any{
			"state":      filepath.Clean(*statePath),
			"high_water": highWater,
			"write":      false,
		}, "", "  ")
		fmt.Println(string(output))
		return
	}

	if *verify {
		if state.Version == 0 {
			fatalf("verify-only requires an existing migration state")
		}
		if err := logmigration.ValidateClickHouseLogSchema(ctx, target); err != nil {
			fatalf("validate target schema: %v", err)
		}
		if err := logmigration.ValidateClickHouseLogTTL(ctx, target, *ttlDays); err != nil {
			fatalf("validate target TTL: %v", err)
		}
	} else {
		if err := logmigration.EnsureClickHouseLogSchema(ctx, target, *ttlDays); err != nil {
			fatalf("ensure target schema: %v", err)
		}
	}
	if state.Version == 0 {
		highWater, err := logmigration.CaptureHighWater(ctx, source)
		if err != nil {
			fatalf("capture high-water: %v", err)
		}
		state = logmigration.State{
			Version:               logmigration.StateVersion,
			ConnectionFingerprint: fingerprint,
			HighWater:             highWater,
		}
		if err := logmigration.SaveState(*statePath, state); err != nil {
			fatalf("save initial state: %v", err)
		}
	}
	if *advance {
		state, err = logmigration.AdvanceHighWater(ctx, source, state)
		if err != nil {
			fatalf("advance high-water: %v", err)
		}
		if err := logmigration.SaveState(*statePath, state); err != nil {
			fatalf("save advanced state: %v", err)
		}
	}
	if !*verify {
		state, err = logmigration.Backfill(ctx, source, target, state, logmigration.BackfillOptions{
			BatchSize: *batchSize,
			OnCommit: func(next logmigration.State) error {
				return logmigration.SaveState(*statePath, next)
			},
		})
		if err != nil {
			fatalf("backfill: %v", err)
		}
	}
	if !state.Complete() {
		fatalf("cannot reconcile incomplete state")
	}
	daily, err := logmigration.Reconcile(ctx, source, target, state.HighWater)
	if err != nil {
		fatalf("reconcile: %v", err)
	}
	output, err := json.MarshalIndent(map[string]any{
		"state":       filepath.Clean(*statePath),
		"high_water":  state.HighWater,
		"rows_copied": state.RowsCopied,
		"daily":       daily,
		"verified":    true,
	}, "", "  ")
	if err != nil {
		fatalf("encode result: %v", err)
	}
	fmt.Println(string(output))
}

// needsStateLock 决定本次运行是否需要独占 state 文件。
//
// verify-only 不写 state，但它读 state.HighWater 并据此对账；并发的回填会改写
// 同一个文件，读到撕裂的高水位就会得出错误的对账结论——而对账是恢复流量的
// 硬门禁，错误的"通过"比跑不完更危险。所以 verify 也必须串行。
//
// init-schema-only 和 dry-run 都不读 state 窗口，无需串行。
func needsStateLock(initOnly, dryRun bool) bool {
	return !initOnly && !dryRun
}
