//go:build integration

package engine

import (
	"io"
	"testing"
)

// TestInheritCopiesParentWorkspace runs a two-job pipeline where the parent
// writes a marker file and the child, declaring `inherit: parent`, asserts
// that the marker is present at /workspace when its steps begin.
func TestInheritCopiesParentWorkspace(t *testing.T) {
	code := Run(Options{
		File:   "../../testdata/inherit.yml",
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if code != ExitSuccess {
		t.Fatalf("Run(testdata/inherit.yml) = %d, want %d", code, ExitSuccess)
	}
}
