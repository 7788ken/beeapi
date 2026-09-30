package service

import (
	"context"
	"errors"
	"sync"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	contentbackupworker "github.com/QuantumNous/new-api/pkg/contentbackupworker"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// The content backup pipeline runs inside beeapi since 2026-09-18 (it used to be a separate
// daemon reached over a Unix socket). This file owns the single runtime handle: main.go
// starts it after InitDB, the capture layer enqueues into it, the controllers read through
// it, and graceful shutdown drains it.

var (
	contentBackupRuntimeMu sync.RWMutex
	contentBackupRuntime   *contentbackupworker.Runtime
)

// StartContentBackupRuntime boots the in-process pipeline. It never fails the gateway for
// a missing or incomplete backup config; only a broken spool volume is an error.
func StartContentBackupRuntime(ctx context.Context, spoolDir string) error {
	if model.DB == nil {
		return errors.New("content backup runtime needs the business database")
	}
	contentBackupRuntimeMu.Lock()
	defer contentBackupRuntimeMu.Unlock()
	if contentBackupRuntime != nil {
		return errors.New("content backup runtime is already running")
	}
	rt, err := contentbackupworker.Start(ctx, contentbackupworker.RuntimeConfig{
		SpoolDir: spoolDir,
		DB:       model.DB,
		Config:   operation_setting.GetContentBackupConfig,
	})
	if err != nil {
		return err
	}
	contentBackupRuntime = rt
	return nil
}

func contentBackupRuntimeHandle() *contentbackupworker.Runtime {
	contentBackupRuntimeMu.RLock()
	defer contentBackupRuntimeMu.RUnlock()
	return contentBackupRuntime
}

// TryEnqueue is the capture layer's Enqueuer hook: non-blocking ownership transfer into the
// runtime's spool queue. false means the caller keeps ownership and must Release.
func TryEnqueue(capture *contentbackup.Capture) bool {
	rt := contentBackupRuntimeHandle()
	if rt == nil {
		return false
	}
	return rt.Enqueue(capture)
}

// ContentBackupStorageNodeID is the volume-bound node identity, "" until the runtime runs.
func ContentBackupStorageNodeID() string {
	rt := contentBackupRuntimeHandle()
	if rt == nil {
		return ""
	}
	return rt.StorageNodeID()
}

// ContentBackupSiteID is the site label from the config; it scopes every DB query.
func ContentBackupSiteID() string {
	return operation_setting.GetContentBackupConfig().SiteLabel
}

// ContentBackupReader returns the site-scoped reader for preview/download/probe, or nil
// when the runtime is not running or no site label is configured yet.
func ContentBackupReader() *contentbackupworker.Reader {
	contentBackupRuntimeMu.RLock()
	override := contentBackupReaderOverride
	contentBackupRuntimeMu.RUnlock()
	if override != nil {
		return override()
	}
	rt := contentBackupRuntimeHandle()
	if rt == nil {
		return nil
	}
	return rt.Reader()
}

var contentBackupReaderOverride func() *contentbackupworker.Reader

// SetContentBackupReaderProvider lets controller tests inject a Reader built over a fake
// remote instead of booting the whole runtime. It returns the restore func.
func SetContentBackupReaderProvider(provider func() *contentbackupworker.Reader) (restore func()) {
	contentBackupRuntimeMu.Lock()
	previous := contentBackupReaderOverride
	contentBackupReaderOverride = provider
	contentBackupRuntimeMu.Unlock()
	return func() {
		contentBackupRuntimeMu.Lock()
		contentBackupReaderOverride = previous
		contentBackupRuntimeMu.Unlock()
	}
}

// ContentBackupRuntimeCounters exposes the process-local handoff counters (for diagnostics).
func ContentBackupRuntimeCounters() contentbackupworker.RuntimeCounters {
	rt := contentBackupRuntimeHandle()
	if rt == nil {
		return contentbackupworker.RuntimeCounters{}
	}
	return rt.Counters()
}

// Shutdown drains queued captures into the spool within ctx and stops the workers. It is
// called after the HTTP server has drained (design doc 4.5), so no new captures arrive.
func Shutdown(ctx context.Context) error {
	contentBackupRuntimeMu.Lock()
	rt := contentBackupRuntime
	contentBackupRuntime = nil
	contentBackupRuntimeMu.Unlock()
	if rt == nil {
		return nil
	}
	return rt.Shutdown(ctx)
}
