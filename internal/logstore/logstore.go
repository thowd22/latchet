// Package logstore owns the persistent per-run log directory. Logs live
// outside the workspace and survive workspace cleanup, so a successful
// run's logs are not deleted.
//
// Resolution order for the base directory: $LATCHET_LOG_DIR, then
// $XDG_STATE_HOME/latchet, then ~/.local/state/latchet.
package logstore

import (
	"fmt"
	"os"
	"path/filepath"
)

// baseDir returns the parent directory under which per-run log directories
// are created.
func baseDir() (string, error) {
	if v := os.Getenv("LATCHET_LOG_DIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "latchet"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "latchet"), nil
}

// Run is the per-run log directory.
type Run struct {
	ID  string // matches the workspace run ID
	Dir string // absolute path to the run's log directory
}

// New creates the run's log directory and best-effort updates a `latest`
// symlink in the base dir that points at the new run.
func New(runID string) (*Run, error) {
	base, err := baseDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating log dir %s: %w", dir, err)
	}

	// Best-effort `latest` symlink. Removing first because os.Symlink fails
	// on Linux if the link already exists; ignoring errors keeps this purely
	// a convenience and never the cause of a failed run.
	latest := filepath.Join(base, "latest")
	_ = os.Remove(latest)
	_ = os.Symlink(runID, latest)

	return &Run{ID: runID, Dir: dir}, nil
}

// OpenJob opens (creating or truncating) the log file for one job and
// returns the file plus its absolute path. The caller is responsible for
// Close.
func (r *Run) OpenJob(jobID string) (*os.File, string, error) {
	path := filepath.Join(r.Dir, jobID+".log")
	f, err := os.Create(path)
	if err != nil {
		return nil, "", fmt.Errorf("creating job log %s: %w", path, err)
	}
	return f, path, nil
}
