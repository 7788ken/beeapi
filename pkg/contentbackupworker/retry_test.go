package contentbackupworker

import (
	"testing"
	"time"
)

func TestContentBackupRetry(t *testing.T) {
	if RetryDelay(1, 0) != time.Minute {
		t.Fatal("first retry")
	}
	if RetryDelay(5, 0) != 6*time.Hour {
		t.Fatal("backoff cap")
	}
	if RetryDelay(16, 0.2) > 6*time.Hour {
		t.Fatal("jitter exceeded cap")
	}
}
