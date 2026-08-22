//go:build windows

package logmigration

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
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
	handle := windows.Handle(lockFile.Fd())
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(
		handle,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("migration state %q is locked by another process: %w", statePath, err)
	}
	return func() error {
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		closeErr := lockFile.Close()
		if unlockErr != nil {
			return unlockErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	}, nil
}
