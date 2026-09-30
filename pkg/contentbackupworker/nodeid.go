package contentbackupworker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

const nodeIDFileName = ".node-id"

// LoadOrCreateNodeID binds the storage node identity to the spool volume itself: the id
// lives in a file inside the spool, is generated once, and is read by every process that
// mounts the volume. That is what makes a blue/green candidate slot the same storage node
// as the slot it replaces without any deployment variable (design doc 4.1 wanted the id
// "bound to the persistent volume"; NODE_NAME is rewritten per slot and cannot be used).
func LoadOrCreateNodeID(spoolDir string) (string, error) {
	if strings.TrimSpace(spoolDir) == "" {
		return "", errors.New("content backup: spool dir is required to derive the node id")
	}
	if err := os.MkdirAll(spoolDir, spoolDirPerm); err != nil {
		return "", fmt.Errorf("content backup: create spool dir: %w", err)
	}
	path := filepath.Join(spoolDir, nodeIDFileName)
	if raw, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(raw))
		if err := contentbackup.ValidateSiteLabel(id); err != nil {
			return "", fmt.Errorf("content backup: node id file %s holds an invalid id %q: %w", path, id, err)
		}
		return id, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("content backup: read node id file: %w", err)
	}
	// "node-" + first 12 hex of a fresh uuid: short enough for logs, unique enough per volume,
	// and inside the site-label charset the DB/remote path rules already enforce.
	id := "node-" + strings.ReplaceAll(contentbackup.NewJobID(), "-", "")[:12]
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("content backup: write node id file: %w", err)
	}
	// O_EXCL-like semantics via link: if two processes race on first start, whoever links
	// first wins and the other reads that winner's id instead of overwriting it.
	if err := os.Link(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if raw, readErr := os.ReadFile(path); readErr == nil {
			return strings.TrimSpace(string(raw)), nil
		}
		return "", fmt.Errorf("content backup: publish node id file: %w", err)
	}
	_ = os.Remove(tmp)
	return id, nil
}
