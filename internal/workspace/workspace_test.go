package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// newRun returns a Run rooted at t.TempDir(). It avoids the random-id path
// used by workspace.New so tests can assert exact paths.
func newRun(t *testing.T) *Run {
	t.Helper()
	return &Run{ID: "test", Root: t.TempDir()}
}

func TestSeedCopiesFilesAndDirs(t *testing.T) {
	r := newRun(t)
	src, _ := r.JobDir("parent")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("hello root"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "deep.txt"), []byte("hello deep"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := r.Seed("child", "parent"); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	dst := filepath.Join(r.Root, "child")
	for _, c := range []struct{ rel, want string }{
		{"root.txt", "hello root"},
		{"nested/deep.txt", "hello deep"},
	} {
		got, err := os.ReadFile(filepath.Join(dst, c.rel))
		if err != nil {
			t.Errorf("ReadFile %s: %v", c.rel, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestSeedPreservesMode(t *testing.T) {
	r := newRun(t)
	src, _ := r.JobDir("parent")
	exe := filepath.Join(src, "script.sh")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := r.Seed("child", "parent"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	info, err := os.Stat(filepath.Join(r.Root, "child", "script.sh"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestSeedPreservesSymlink(t *testing.T) {
	r := newRun(t)
	src, _ := r.JobDir("parent")
	if err := os.WriteFile(filepath.Join(src, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Relative symlink — common in repos; should be preserved verbatim.
	if err := os.Symlink("target.txt", filepath.Join(src, "link")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := r.Seed("child", "parent"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	dst := filepath.Join(r.Root, "child", "link")
	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != "target.txt" {
		t.Errorf("symlink target = %q, want %q", got, "target.txt")
	}
}

func TestSeedPreservesEmptyDir(t *testing.T) {
	r := newRun(t)
	src, _ := r.JobDir("parent")
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := r.Seed("child", "parent"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	info, err := os.Stat(filepath.Join(r.Root, "child", "empty"))
	if err != nil || !info.IsDir() {
		t.Errorf("empty dir missing or wrong type: info=%v err=%v", info, err)
	}
}

func TestSeedMissingSource(t *testing.T) {
	r := newRun(t)
	if err := r.Seed("child", "ghost"); err == nil {
		t.Fatal("expected error for missing source")
	}
}
