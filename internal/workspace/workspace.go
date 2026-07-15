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
	"io"
	"io/fs"
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

// CacheRoot resolves and pre-creates the persistent job cache directory,
// bind-mounted at /cache into jobs that declare `cache: true`. Resolution:
// LATCHET_CACHE_ROOT (set directly or via the global config's cache_root),
// else <user cache dir>/latchet/jobcache. Unlike run workspaces it is never
// cleaned up — that is the point. World-writable because the container user
// (rootless subuid mappings) may differ from the latchet user.
func CacheRoot() (string, error) {
	dir := os.Getenv("LATCHET_CACHE_ROOT")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolving job cache root: %w", err)
		}
		dir = filepath.Join(base, "latchet", "jobcache")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating job cache root %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o777)
	return dir, nil
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

// Seed copies the host workspace of srcJobID into dstJobID, so the
// destination job's container sees the source job's files at /workspace
// when it starts. Used by jobs declaring `inherit:` in latchet.yml.
//
// Regular files, directories (including empty ones), and symlinks are
// copied. Mode bits are preserved; timestamps and ownership are not.
// Symlinks are preserved verbatim (cp -P semantics), not followed.
// Special files (devices, sockets, fifos) cause Seed to fail loudly with
// the offending path so surprising input never silently disappears.
//
// Concurrency: by the time Seed runs, the parent has produced a terminal
// result through the scheduler (because `inherit` requires `needs`
// membership). The parent's directory is quiescent; multiple sibling
// jobs may safely Seed from the same parent concurrently — all are
// readers of a non-writer.
func (r *Run) Seed(dstJobID, srcJobID string) error {
	src := filepath.Join(r.Root, srcJobID)
	dst := filepath.Join(r.Root, dstJobID)

	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("seeding %s from %s: %w", dst, src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("seeding %s from %s: %w", dst, src, err)
	}

	walkErr := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		mode := d.Type()

		switch {
		case mode.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target) // Symlink fails if target exists
			return os.Symlink(link, target)
		case mode.IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyRegular(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported file type at %s (mode %v)", path, mode)
		}
	})
	if walkErr != nil {
		return fmt.Errorf("seeding %s from %s: %w", dst, src, walkErr)
	}
	return nil
}

// copyRegular copies one regular file, creating dst with mode and overwriting
// any existing content.
func copyRegular(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
