//go:build !windows

package workspace

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestSeedRejectsFIFO confirms that special files (here: a named pipe)
// fail the seed rather than being silently dropped.
func TestSeedRejectsFIFO(t *testing.T) {
	r := newRun(t)
	src, _ := r.JobDir("parent")
	fifo := filepath.Join(src, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	err := r.Seed("child", "parent")
	if err == nil {
		t.Fatal("expected error for fifo")
	}
	if !strings.Contains(err.Error(), "unsupported file type") {
		t.Errorf("error %q does not mention unsupported file type", err)
	}
}
