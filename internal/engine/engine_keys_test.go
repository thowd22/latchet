package engine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// keyFixtureRepo builds a local git repo containing testdata/keys (the greet
// key) tagged v1, for exercising the uses: fetch path without a network.
// Skips the test when git is missing; no container runtime is needed.
func keyFixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	gitRun("init", "-q", "-b", "main")
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "keys", "greet", "key.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "greet", "key.yml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "-A")
	gitRun("commit", "-q", "-m", "greet key")
	gitRun("tag", "v1")
	return dir
}

func writeWorkflow(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "latchet.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateUsesKey(t *testing.T) {
	repo := keyFixtureRepo(t)
	t.Setenv("LATCHET_KEYS_CACHE", t.TempDir())

	valid := "jobs:\n  a:\n    container: alpine\n    steps:\n      - uses: " + repo + "//greet@v1\n        with: {who: world}\n"
	if code := Validate(Options{File: writeWorkflow(t, valid), Stdout: io.Discard, Stderr: io.Discard}); code != ExitSuccess {
		t.Errorf("valid uses workflow: exit %d, want %d", code, ExitSuccess)
	}

	// The fetched key's declared inputs are enforced at validation time.
	badWith := "jobs:\n  a:\n    container: alpine\n    steps:\n      - uses: " + repo + "//greet@v1\n        with: {bogus: x}\n"
	if code := Validate(Options{File: writeWorkflow(t, badWith), Stdout: io.Discard, Stderr: io.Discard}); code != ExitConfig {
		t.Errorf("undeclared with: exit %d, want %d", code, ExitConfig)
	}

	// A branch pin is a spec error (exit 2)...
	branch := "jobs:\n  a:\n    container: alpine\n    steps:\n      - uses: " + repo + "//greet@main\n        with: {who: world}\n"
	if code := Validate(Options{File: writeWorkflow(t, branch), Stdout: io.Discard, Stderr: io.Discard}); code != ExitConfig {
		t.Errorf("branch ref: exit %d, want %d", code, ExitConfig)
	}

	// ...while an unreachable repo is an infrastructure error (exit 3).
	gone := "jobs:\n  a:\n    container: alpine\n    steps:\n      - uses: " + filepath.Join(t.TempDir(), "nope") + "//greet@v1\n        with: {who: world}\n"
	if code := Validate(Options{File: writeWorkflow(t, gone), Stdout: io.Discard, Stderr: io.Discard}); code != ExitInfra {
		t.Errorf("unreachable repo: exit %d, want %d", code, ExitInfra)
	}
}
