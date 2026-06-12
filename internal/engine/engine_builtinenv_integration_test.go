//go:build integration

package engine

import (
	"io"
	"testing"
)

// TestBuiltinEnvInjected runs a workflow whose single step asserts that the
// built-in LATCHET_* vars are present in the container env and that a
// workflow-level `env:` value overrides the injected default (the overridable
// invariant the env-merge ordering guarantees).
func TestBuiltinEnvInjected(t *testing.T) {
	code := Run(Options{
		File:   "../../testdata/builtinenv.yml",
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if code != ExitSuccess {
		t.Fatalf("Run(testdata/builtinenv.yml) = %d, want %d", code, ExitSuccess)
	}
}
