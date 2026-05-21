//go:build integration

// Integration tests exercise a real container runtime (docker or podman) and
// are gated behind the `integration` build tag so `go test ./...` stays fast
// and hermetic. Run them with: go test -tags integration ./internal/engine/...
package engine

import "testing"

// TestRunSampleWorkflow runs the bundled three-job sample workflow end to end
// and expects every job to succeed.
func TestRunSampleWorkflow(t *testing.T) {
	if code := Run(Options{File: "../../testdata/latchet.yml"}); code != ExitSuccess {
		t.Fatalf("Run(testdata/latchet.yml) = %d, want %d", code, ExitSuccess)
	}
}
