package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thowd22/latchet/internal/dag"
)

// graph builds a dag.Graph from a deps map, failing the test on error.
func graph(t *testing.T, deps map[string][]string) *dag.Graph {
	t.Helper()
	g, err := dag.Build(deps)
	if err != nil {
		t.Fatalf("dag.Build: %v", err)
	}
	return g
}

// recordOrder returns a RunJobFn that appends the job ID to a shared slice
// (under mu) when invoked, returns success, and never errors.
func recordOrder(mu *sync.Mutex, order *[]string) RunJobFn {
	return func(_ context.Context, id string) (Result, error) {
		mu.Lock()
		*order = append(*order, id)
		mu.Unlock()
		return Result{Status: StatusSuccess}, nil
	}
}

func TestSequentialChain(t *testing.T) {
	g := graph(t, map[string][]string{
		"a": nil, "b": {"a"}, "c": {"b"},
	})
	var mu sync.Mutex
	var order []string
	results, err := Run(context.Background(), g, Options{
		MaxParallel: 1,
		RunJob:      recordOrder(&mu, &order),
	})
	if err != nil {
		t.Fatalf("unexpected infra err: %v", err)
	}
	want := []string{"a", "b", "c"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
	for _, id := range want {
		if results[id] == nil || results[id].Status != StatusSuccess {
			t.Errorf("results[%s] = %+v, want success", id, results[id])
		}
	}
}

func TestParallelDiamondBarrier(t *testing.T) {
	// a -> {b, c} -> d. With MaxParallel=2, b and c must be in runJob
	// simultaneously: the WaitGroup forces them to rendezvous before either
	// can return.
	g := graph(t, map[string][]string{
		"a": nil, "b": {"a"}, "c": {"a"}, "d": {"b", "c"},
	})

	var partners sync.WaitGroup
	partners.Add(2)
	gate := make(chan struct{})
	go func() {
		partners.Wait()
		close(gate)
	}()

	runJob := func(_ context.Context, id string) (Result, error) {
		if id == "b" || id == "c" {
			partners.Done()
			select {
			case <-gate:
			case <-time.After(2 * time.Second):
				return Result{}, errors.New("partner never arrived — jobs not parallel")
			}
		}
		return Result{Status: StatusSuccess}, nil
	}

	results, err := Run(context.Background(), g, Options{
		MaxParallel: 2,
		RunJob:      runJob,
	})
	if err != nil {
		t.Fatalf("unexpected infra err: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if results[id] == nil || results[id].Status != StatusSuccess {
			t.Errorf("results[%s] = %+v, want success", id, results[id])
		}
	}
}

func TestMaxParallelRespected(t *testing.T) {
	// 5 independent jobs; with MaxParallel=2 only two should run at once.
	deps := map[string][]string{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		deps[id] = nil
	}
	g := graph(t, deps)

	var inFlight atomic.Int32
	var maxSeen atomic.Int32
	runJob := func(_ context.Context, id string) (Result, error) {
		now := inFlight.Add(1)
		for {
			cur := maxSeen.Load()
			if now <= cur || maxSeen.CompareAndSwap(cur, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		return Result{Status: StatusSuccess}, nil
	}

	if _, err := Run(context.Background(), g, Options{MaxParallel: 2, RunJob: runJob}); err != nil {
		t.Fatalf("unexpected infra err: %v", err)
	}
	if got := maxSeen.Load(); got > 2 {
		t.Fatalf("MaxParallel=2 exceeded: saw %d in flight", got)
	}
}

func TestFailurePropagation(t *testing.T) {
	// a -> {b, c}; a fails. b and c must both be skipped with reason "a failed".
	g := graph(t, map[string][]string{
		"a": nil, "b": {"a"}, "c": {"a"},
	})
	var skipped []string
	var mu sync.Mutex

	runJob := func(_ context.Context, id string) (Result, error) {
		if id == "a" {
			return Result{Status: StatusFailed, Detail: "boom"}, nil
		}
		return Result{Status: StatusSuccess}, nil
	}
	onSkip := func(id, _ string) {
		mu.Lock()
		skipped = append(skipped, id)
		mu.Unlock()
	}

	results, err := Run(context.Background(), g, Options{
		MaxParallel: 1, RunJob: runJob, OnSkip: onSkip,
	})
	if err != nil {
		t.Fatalf("unexpected infra err: %v", err)
	}
	if results["a"].Status != StatusFailed {
		t.Errorf("a: %+v, want failed", results["a"])
	}
	for _, id := range []string{"b", "c"} {
		if results[id].Status != StatusSkipped {
			t.Errorf("%s: %+v, want skipped", id, results[id])
		}
		if results[id].Detail != "a failed" {
			t.Errorf("%s skip detail = %q, want %q", id, results[id].Detail, "a failed")
		}
	}
	sort.Strings(skipped)
	if fmt.Sprint(skipped) != fmt.Sprint([]string{"b", "c"}) {
		t.Errorf("OnSkip called for %v, want [b c]", skipped)
	}
}

func TestInfraErrorCancelsRun(t *testing.T) {
	// a -> b. a returns an infra error; b must end up skipped.
	g := graph(t, map[string][]string{
		"a": nil, "b": {"a"},
	})
	runJob := func(_ context.Context, id string) (Result, error) {
		if id == "a" {
			return Result{}, errors.New("docker is dead")
		}
		t.Errorf("job %s should not have run", id)
		return Result{Status: StatusSuccess}, nil
	}
	results, err := Run(context.Background(), g, Options{MaxParallel: 1, RunJob: runJob})
	if err == nil || err.Error() != "docker is dead" {
		t.Fatalf("infra err = %v, want \"docker is dead\"", err)
	}
	if results["a"].Status != StatusSkipped {
		t.Errorf("a status = %v, want skipped (infra)", results["a"].Status)
	}
	if results["b"].Status != StatusSkipped {
		t.Errorf("b status = %v, want skipped", results["b"].Status)
	}
}

func TestIndependentJobsRunDespiteFailure(t *testing.T) {
	// a and b are independent; a fails; b must still run.
	g := graph(t, map[string][]string{
		"a": nil, "b": nil,
	})
	ran := make(map[string]bool)
	var mu sync.Mutex
	runJob := func(_ context.Context, id string) (Result, error) {
		mu.Lock()
		ran[id] = true
		mu.Unlock()
		if id == "a" {
			return Result{Status: StatusFailed, Detail: "x"}, nil
		}
		return Result{Status: StatusSuccess}, nil
	}
	results, err := Run(context.Background(), g, Options{MaxParallel: 1, RunJob: runJob})
	if err != nil {
		t.Fatalf("unexpected infra err: %v", err)
	}
	if !ran["b"] {
		t.Fatal("b should still run even though a failed")
	}
	if results["a"].Status != StatusFailed || results["b"].Status != StatusSuccess {
		t.Errorf("results = %+v", results)
	}
}
