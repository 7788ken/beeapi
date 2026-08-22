package logmigration

import (
	"path/filepath"
	"testing"
)

func TestStateLockRejectsConcurrentExecutor(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "migration-state.json")
	release, err := AcquireStateLock(statePath)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if _, err := AcquireStateLock(statePath); err == nil {
		t.Fatal("concurrent state lock unexpectedly succeeded")
	}
	if err := release(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	releaseAgain, err := AcquireStateLock(statePath)
	if err != nil {
		t.Fatalf("reacquire released lock: %v", err)
	}
	if err := releaseAgain(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}
