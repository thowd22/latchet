//go:build integration

package engine

import (
	"io"
	"testing"
	"time"
)

// TestParallelSpeedup runs the parallel demo workflow at MaxParallel=1 and
// MaxParallel=3 and asserts the parallel run is materially faster. Three
// independent jobs each sleep ~3s; sequential is at least ~9s of sleep, and
// parallel should be ~3s of sleep, so a 60% ceiling on the parallel/sequential
// ratio is generous even accounting for container startup overhead.
func TestParallelSpeedup(t *testing.T) {
	const path = "../../testdata/parallel.yml"

	t0 := time.Now()
	if code := Run(Options{File: path, MaxParallel: 1, Stdout: io.Discard, Stderr: io.Discard}); code != ExitSuccess {
		t.Fatalf("sequential run failed: code %d", code)
	}
	seq := time.Since(t0)

	t0 = time.Now()
	if code := Run(Options{File: path, MaxParallel: 3, Stdout: io.Discard, Stderr: io.Discard}); code != ExitSuccess {
		t.Fatalf("parallel run failed: code %d", code)
	}
	par := time.Since(t0)

	t.Logf("sequential: %s, parallel(N=3): %s", seq, par)

	limit := time.Duration(float64(seq) * 0.6)
	if par > limit {
		t.Fatalf("parallel(N=3) took %s; expected <= %s (60%% of sequential %s)", par, limit, seq)
	}
}
