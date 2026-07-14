//go:build integration

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thowd22/latchet/internal/provenance"
)

// TestRunKeyWorkflow runs a job that uses: the greet fixture key end-to-end
// (real container runtime + git) and checks the key lands in provenance
// resolvedDependencies SHA-pinned.
func TestRunKeyWorkflow(t *testing.T) {
	repo := keyFixtureRepo(t)
	t.Setenv("LATCHET_KEYS_CACHE", t.TempDir())
	logDir := t.TempDir()
	t.Setenv("LATCHET_LOG_DIR", logDir)

	wf := "jobs:\n  a:\n    container: alpine:3.19\n    steps:\n      - uses: " + repo + "//greet@v1\n        with: {who: world}\n"
	if code := Run(Options{File: writeWorkflow(t, wf)}); code != ExitSuccess {
		t.Fatalf("Run = %d, want %d", code, ExitSuccess)
	}

	st, err := provenance.Load(filepath.Join(logDir, "latest", "provenance.json"))
	if err != nil {
		t.Fatalf("loading provenance: %v", err)
	}
	ks := st.ResolvedKeys()
	uri, ok := ks[repo+"//greet@v1"]
	if !ok {
		t.Fatalf("key not in resolvedDependencies: %v", ks)
	}
	// git+<repo>//greet@<40-hex sha>
	at := strings.LastIndex(uri, "@")
	if !strings.HasPrefix(uri, "git+"+repo+"//greet@") || at < 0 || len(uri[at+1:]) != 40 {
		t.Errorf("key URI not SHA-pinned: %q", uri)
	}
	if _, err := os.Stat(filepath.Join(logDir, "latest")); err != nil {
		t.Errorf("latest symlink: %v", err)
	}
}
