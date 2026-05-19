// Package workspace manages the host directories a run uses.
//
// Every run gets one root directory; every job gets a sub-directory beneath
// it, which is bind-mounted into that job's container as /workspace. Steps in
// the same job therefore share a directory; jobs do not share with each other.
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Run owns the on-disk workspace for a single workflow execution.
type Run struct {
	ID   string // sortable, e.g. 20260518T203200-a1b2c3
	Root string // absolute path to the run's root directory
}

// rootDir is where run directories are created. LATCHET_WORKSPACE_ROOT
// overrides the default of <tempdir>/latchet.
func rootDir() string {
	if v := os.Getenv("LATCHET_WORKSPACE_ROOT"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "latchet")
}

// New allocates a fresh run directory.
func New() (*Run, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("generating run id: %w", err)
	}
	id := time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(suffix)

	root := filepath.Join(rootDir(), id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("creating workspace %s: %w", root, err)
	}
	return &Run{ID: id, Root: root}, nil
}

// JobDir creates (if needed) and returns the workspace directory for one job.
func (r *Run) JobDir(jobID string) (string, error) {
	dir := filepath.Join(r.Root, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating job workspace %s: %w", dir, err)
	}
	return dir, nil
}

// Cleanup removes the run directory. It is kept — and the path returned non-
// empty — when the run failed (for debugging) or when LATCHET_KEEP_WORKSPACE=1
// is set. The returned string is the retained path, or "" if removed.
func (r *Run) Cleanup(failed bool) string {
	if failed || os.Getenv("LATCHET_KEEP_WORKSPACE") == "1" {
		return r.Root
	}
	os.RemoveAll(r.Root)
	return ""
}
