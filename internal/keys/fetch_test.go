package keys

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thowd22/latchet/internal/config"
)

// gitOrSkip skips the test when git is not on PATH; these tests need git but
// no container runtime.
func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixtureRepo builds a local key repo: greet/key.yml committed and tagged v1
// (annotated) plus a `dev` branch. Returns the repo path and the HEAD SHA.
func fixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	keyYML := "inputs:\n  who: {required: true}\n  punct: {default: \"!\"}\nsteps:\n  - run: echo hi $who$punct\n"
	if err := os.WriteFile(filepath.Join(dir, "greet", "key.yml"), []byte(keyYML), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "greet key")
	run(t, dir, "tag", "-a", "v1", "-m", "v1")
	run(t, dir, "branch", "dev")
	return dir, run(t, dir, "rev-parse", "HEAD")
}

func TestResolveTagAndBranch(t *testing.T) {
	gitOrSkip(t)
	repo, head := fixtureRepo(t)
	ctx := context.Background()

	// Annotated tag resolves to the peeled commit.
	sha, err := resolve(ctx, Ref{Raw: "r", URL: repo, RefName: "v1"})
	if err != nil {
		t.Fatalf("resolve tag: %v", err)
	}
	if sha != head {
		t.Errorf("tag resolved to %s, want %s", sha, head)
	}

	// SHA passthrough: no network, no repo needed.
	sha2, err := resolve(ctx, Ref{Raw: "r", URL: "git@nowhere:none", RefName: head})
	if err != nil || sha2 != head {
		t.Errorf("sha passthrough: got %s, %v", sha2, err)
	}

	// Branch refs are rejected.
	if _, err := resolve(ctx, Ref{Raw: "r", URL: repo, RefName: "dev"}); err == nil || !strings.Contains(err.Error(), "resolves to a branch") {
		t.Errorf("branch: want rejection, got %v", err)
	}

	// Unknown tags are reported as such.
	if _, err := resolve(ctx, Ref{Raw: "r", URL: repo, RefName: "v9"}); err == nil || !strings.Contains(err.Error(), "no tag") {
		t.Errorf("unknown tag: want 'no tag', got %v", err)
	}
}

func TestFetchCachesBySHA(t *testing.T) {
	gitOrSkip(t)
	repo, head := fixtureRepo(t)
	cache := t.TempDir()
	ctx := context.Background()
	ref := Ref{Raw: "r", URL: repo, Subpath: "greet", RefName: "v1"}

	dir, err := fetch(ctx, ref, head, cache)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if dir != filepath.Join(cache, head) {
		t.Errorf("fetched to %s, want %s", dir, filepath.Join(cache, head))
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git not stripped from cache entry")
	}

	// Cache hit works with the origin gone (offline SHA-pinned fetch).
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(ctx, ref, head, cache); err != nil {
		t.Errorf("cache hit after origin removed: %v", err)
	}
}

func TestLoadKeyYML(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("good/key.yml", "inputs:\n  who: {required: true}\nsteps:\n  - run: echo $who\n")
	fn, err := load(Ref{Raw: "r", Subpath: "good"}, dir)
	if err != nil {
		t.Fatalf("good key: %v", err)
	}
	if fn.Inputs["who"] == nil || !fn.Inputs["who"].Required || len(fn.Steps) != 1 {
		t.Errorf("good key parsed wrong: %+v", fn)
	}

	cases := []struct{ name, rel, content, wantSub string }{
		{"unknown field", "unk", "inputs: {}\nsteps: [{run: echo}]\ncontainer: alpine\n", "parsing key.yml"},
		{"call in body", "call", "steps: [{call: f}]\n", "must be a plain run:"},
		{"uses in body", "uses", "steps: [{uses: \"x//y@v1\"}]\n", "must be a plain run:"},
		{"if in body", "if", "steps: [{run: echo, if: \"$X\"}]\n", "cannot have if/elif/else"},
		{"no steps", "empty", "inputs: {}\n", "has no steps"},
		{"bad input name", "badin", "inputs:\n  bad-name: {}\nsteps: [{run: echo}]\n", "not a valid env var name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			write(tc.rel+"/key.yml", tc.content)
			_, err := load(Ref{Raw: "r", Subpath: tc.rel}, dir)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}

	if _, err := load(Ref{Raw: "r", Subpath: "nope"}, dir); err == nil || !strings.Contains(err.Error(), "no key.yml") {
		t.Errorf("missing key.yml: got %v", err)
	}
}

func TestResolveAll(t *testing.T) {
	gitOrSkip(t)
	repo, head := fixtureRepo(t)
	t.Setenv("LATCHET_KEYS_CACHE", t.TempDir())
	raw := repo + "//greet@v1"

	wf := &config.Workflow{Jobs: map[string]*config.Job{
		"a": {Steps: []*config.Step{
			{Uses: raw, With: map[string]string{"who": "x"}},
			{Uses: raw, With: map[string]string{"who": "y"}}, // dedup
			{Run: "echo plain"},
		}},
	}}
	fns, resolved, err := ResolveAll(context.Background(), wf, nil)
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	if len(fns) != 1 || fns[raw] == nil {
		t.Fatalf("want 1 resolved key, got %v", fns)
	}
	if want := "git+" + repo + "//greet@" + head; resolved[raw] != want {
		t.Errorf("resolved URI %q, want %q", resolved[raw], want)
	}

	// No uses: steps -> nothing fetched, no cache dir touched.
	fns, resolved, err = ResolveAll(context.Background(), &config.Workflow{Jobs: map[string]*config.Job{"a": {Steps: []*config.Step{{Run: "echo"}}}}}, nil)
	if err != nil || fns != nil || resolved != nil {
		t.Errorf("no-uses workflow: got %v %v %v", fns, resolved, err)
	}
}
