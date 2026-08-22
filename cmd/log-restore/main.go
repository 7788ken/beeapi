package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/QuantumNous/new-api/internal/logmigration"
)

func fatalf(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "log-restore: "+format+"\n", values...)
	os.Exit(1)
}

func main() {
	var (
		statePath = flag.String("state", "log-restore-state.json", "resume state file")
		from      = flag.Int64("from", 0, "cutover audit timestamp; runtime events are identified by event_id")
		batchSize = flag.Int("batch-size", 1000, "rows per restore transaction")
		dryRun    = flag.Bool("dry-run", false, "validate connections and print captured high-water without writing")
		verify    = flag.Bool("verify-only", false, "only reconcile an existing completed restore state")
	)
	flag.Parse()

	if *from <= 0 {
		fatalf("from must be a positive Unix timestamp")
	}
	if *batchSize <= 0 {
		fatalf("batch-size must be positive")
	}
	if !*dryRun && !*verify {
		releaseLock, err := logmigration.AcquireStateLock(*statePath)
		if err != nil {
			fatalf("acquire state lock: %v", err)
		}
		defer func() {
			if err := releaseLock(); err != nil {
				fmt.Fprintf(os.Stderr, "log-restore: release state lock: %v\n", err)
			}
		}()
	}

	sourceDSN := os.Getenv("LOG_MIGRATION_SOURCE_DSN")
	if sourceDSN == "" {
		sourceDSN = os.Getenv("SQL_DSN")
	}
	clickHouseDSN := os.Getenv("LOG_SQL_DSN")
	if !logmigration.IsClickHouseDSN(clickHouseDSN) {
		fatalf("LOG_SQL_DSN must be ClickHouse")
	}
	sqlitePath := os.Getenv("LOG_MIGRATION_SOURCE_SQLITE_PATH")
	if sqlitePath == "" {
		sqlitePath = os.Getenv("SQLITE_PATH")
	}
	relational, err := logmigration.OpenSource(sourceDSN, sqlitePath)
	if err != nil {
		fatalf("open relational log database: %v", err)
	}
	clickHouse, err := logmigration.OpenClickHouse(clickHouseDSN)
	if err != nil {
		fatalf("open ClickHouse: %v", err)
	}
	relationalSQL, err := relational.DB()
	if err != nil {
		fatalf("relational sql handle: %v", err)
	}
	defer relationalSQL.Close()
	clickHouseSQL, err := clickHouse.DB()
	if err != nil {
		fatalf("ClickHouse sql handle: %v", err)
	}
	defer clickHouseSQL.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := relationalSQL.PingContext(ctx); err != nil {
		fatalf("relational ping: %v", err)
	}
	if err := clickHouseSQL.PingContext(ctx); err != nil {
		fatalf("ClickHouse ping: %v", err)
	}
	if err := logmigration.ValidateClickHouseLogSchema(ctx, clickHouse); err != nil {
		fatalf("validate ClickHouse schema: %v", err)
	}

	fingerprint := logmigration.ConnectionFingerprint(clickHouseDSN, sourceDSN, sqlitePath)
	state, err := logmigration.LoadRestoreState(*statePath)
	if err != nil {
		fatalf("load state: %v", err)
	}
	if state.Version != 0 {
		if state.ConnectionFingerprint != fingerprint {
			fatalf("state belongs to different ClickHouse/relational connections")
		}
		if state.From != *from {
			fatalf("state cutover timestamp is %d, not %d", state.From, *from)
		}
	}

	if *dryRun {
		highWater, err := logmigration.CaptureClickHouseHighWater(ctx, clickHouse, *from)
		if err != nil {
			fatalf("capture high-water: %v", err)
		}
		output, _ := json.MarshalIndent(map[string]any{
			"state":      filepath.Clean(*statePath),
			"from":       *from,
			"high_water": highWater,
			"write":      false,
		}, "", "  ")
		fmt.Println(string(output))
		return
	}

	if *verify {
		if state.Version == 0 {
			fatalf("verify-only requires an existing restore state")
		}
	} else {
		if err := logmigration.EnsureRestoreLedger(ctx, relational); err != nil {
			fatalf("ensure restore ledger: %v", err)
		}
		if state.Version == 0 {
			highWater, err := logmigration.CaptureClickHouseHighWater(ctx, clickHouse, *from)
			if err != nil {
				fatalf("capture high-water: %v", err)
			}
			state = logmigration.RestoreState{
				Version:               logmigration.StateVersion,
				ConnectionFingerprint: fingerprint,
				From:                  *from,
				HighWater:             highWater,
			}
			if err := logmigration.SaveRestoreState(*statePath, state); err != nil {
				fatalf("save initial state: %v", err)
			}
		}
		state, err = logmigration.RestoreClickHouseRows(
			ctx,
			clickHouse,
			relational,
			state,
			logmigration.RestoreOptions{
				BatchSize: *batchSize,
				OnCommit: func(next logmigration.RestoreState) error {
					return logmigration.SaveRestoreState(*statePath, next)
				},
			},
		)
		if err != nil {
			fatalf("restore: %v", err)
		}
	}

	if !state.Complete() {
		fatalf("cannot reconcile incomplete restore state")
	}
	daily, err := logmigration.ReconcileRestored(ctx, clickHouse, relational, state.From, state.HighWater)
	if err != nil {
		fatalf("reconcile: %v", err)
	}
	output, err := json.MarshalIndent(map[string]any{
		"state":         filepath.Clean(*statePath),
		"from":          state.From,
		"high_water":    state.HighWater,
		"rows_restored": state.RowsRestored,
		"daily":         daily,
		"verified":      true,
	}, "", "  ")
	if err != nil {
		fatalf("encode result: %v", err)
	}
	fmt.Println(string(output))
}
