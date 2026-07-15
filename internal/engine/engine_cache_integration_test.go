//go:build integration

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCacheMountPersistsAcrossRuns proves the /cache mount round-trips: run 1
// writes a file into /cache, run 2 (a fresh run id, same LATCHET_CACHE_ROOT)
// reads it back, and the cache dir survives on the host afterwards.
func TestCacheMountPersistsAcrossRuns(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("LATCHET_CACHE_ROOT", cacheRoot)
	t.Setenv("LATCHET_LOG_DIR", t.TempDir())

	write := "jobs:\n  warm:\n    container: alpine:3.19\n    cache: true\n    steps:\n      - run: echo hello-from-run-1 > \"$LATCHET_CACHE/marker\"\n"
	if code := Run(Options{File: writeWorkflow(t, write)}); code != ExitSuccess {
		t.Fatalf("run 1 = %d, want %d", code, ExitSuccess)
	}

	read := "jobs:\n  use:\n    container: alpine:3.19\n    cache: true\n    steps:\n      - run: grep -q hello-from-run-1 \"$LATCHET_CACHE/marker\" && echo CACHE-ROUNDTRIP-OK\n"
	if code := Run(Options{File: writeWorkflow(t, read)}); code != ExitSuccess {
		t.Fatalf("run 2 = %d, want %d (cache did not persist?)", code, ExitSuccess)
	}

	// The marker survives on the host — the cache root is not run-scoped.
	if _, err := os.Stat(filepath.Join(cacheRoot, "marker")); err != nil {
		t.Errorf("cache root lost the marker after runs: %v", err)
	}
}
