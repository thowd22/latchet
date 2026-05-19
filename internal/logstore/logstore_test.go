package logstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseDirHonorsLATCHET_LOG_DIR(t *testing.T) {
	t.Setenv("LATCHET_LOG_DIR", "/tmp/latchet-override")
	t.Setenv("XDG_STATE_HOME", "/tmp/should-be-ignored")
	got, err := baseDir()
	if err != nil {
		t.Fatalf("baseDir: %v", err)
	}
	if got != "/tmp/latchet-override" {
		t.Errorf("baseDir = %q, want /tmp/latchet-override", got)
	}
}

func TestBaseDirHonorsXDG(t *testing.T) {
	t.Setenv("LATCHET_LOG_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/tmp/myxdg")
	got, err := baseDir()
	if err != nil {
		t.Fatalf("baseDir: %v", err)
	}
	if got != "/tmp/myxdg/latchet" {
		t.Errorf("baseDir = %q, want /tmp/myxdg/latchet", got)
	}
}

func TestBaseDirFallsBackToHome(t *testing.T) {
	t.Setenv("LATCHET_LOG_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	got, err := baseDir()
	if err != nil {
		t.Fatalf("baseDir: %v", err)
	}
	if !strings.Contains(got, ".local/state/latchet") {
		t.Errorf("baseDir = %q, want path containing .local/state/latchet", got)
	}
}

func TestNewCreatesDirAndOpenJobWritesFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LATCHET_LOG_DIR", base)

	r, err := New("runX")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Dir != filepath.Join(base, "runX") {
		t.Errorf("Run.Dir = %q", r.Dir)
	}

	f, path, err := r.OpenJob("build")
	if err != nil {
		t.Fatalf("OpenJob: %v", err)
	}
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f.Close()

	wantPath := filepath.Join(base, "runX", "build.log")
	if path != wantPath {
		t.Errorf("OpenJob path = %q, want %q", path, wantPath)
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("file contents = %q", got)
	}
}

func TestLatestSymlinkUpdatesPerRun(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LATCHET_LOG_DIR", base)

	if _, err := New("run1"); err != nil {
		t.Fatalf("New run1: %v", err)
	}
	if _, err := New("run2"); err != nil {
		t.Fatalf("New run2: %v", err)
	}

	target, err := os.Readlink(filepath.Join(base, "latest"))
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "run2" {
		t.Errorf("latest -> %q, want run2", target)
	}
}
