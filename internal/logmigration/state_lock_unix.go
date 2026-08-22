//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package logmigration

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func AcquireStateLock(statePath string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return nil, err
	}
	lockPath := statePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("migration state %q is locked by another process: %w", statePath, err)
	}
	return func() error {
		unlockErr := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		closeErr := lockFile.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
