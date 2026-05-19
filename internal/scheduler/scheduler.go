// Package scheduler executes jobs from a dag.Graph, respecting `needs`
// edges and limiting concurrency to a caller-supplied cap.
//
// The package is intentionally engine-free: it knows nothing about
// containers, workspaces, or YAML. The caller supplies a RunJobFn that
// performs the actual work for one job ID and returns a terminal Status,
// and the scheduler handles ordering, skip propagation, parallelism, and
// infrastructure-error cancellation.
package scheduler

import (
	"context"
	"fmt"
	"sort"

	"github.com/thowd22/latchet/internal/dag"
)

// Status is the terminal outcome of a job.
type Status string

const (
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Result is one job's outcome.
type Result struct {
	ID     string
	Status Status
	Detail string // failing-step description, or skip reason
}

// RunJobFn does the work for one job. A non-nil error signals an
// infrastructure failure (image pull, container create, exec dispatch)
// that should abort the whole run; a step exiting non-zero must be
// returned as a Result with Status == StatusFailed instead.
type RunJobFn func(ctx context.Context, jobID string) (Result, error)

// SkipFn is called once per job that the scheduler skips because one of
// its needs did not succeed. It runs on the scheduler's controller
// goroutine, so it is safe for the caller to write to shared state.
type SkipFn func(jobID, reason string)

// Options configures one scheduler run.
type Options struct {
	MaxParallel int      // >=1; values <1 are treated as 1
	RunJob      RunJobFn // required
	OnSkip      SkipFn   // optional
}

// Run executes every job in g, respecting needs and capping concurrent jobs
// at MaxParallel. It returns a map keyed by job ID containing every job's
// result. If any RunJob call returns an error, Run cancels in-flight work,
// waits for it to drain, marks every not-yet-run job as skipped, and
// returns the first infrastructure error as the second return.
//
// Jobs are dispatched in deterministic order: whenever several are ready at
// once, the alphabetically smallest goes first. The result map is keyed by
// ID, so the caller can read it in topological (or any other) order.
func Run(parent context.Context, g *dag.Graph, opts Options) (map[string]*Result, error) {
	max := opts.MaxParallel
	if max < 1 {
		max = 1
	}

	// Working copy of indegree so the input Graph is not mutated.
	indeg := make(map[string]int, len(g.Indegree))
	for k, v := range g.Indegree {
		indeg[k] = v
	}

	results := make(map[string]*Result, len(g.Order))
	sem := make(chan struct{}, max)

	type completion struct {
		id  string
		res Result
		err error
	}
	done := make(chan completion, len(g.Order))

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var infraErr error
	var ready []string
	for id, n := range indeg {
		if n == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	advance := func(completedID string) {
		for _, dep := range g.Dependents[completedID] {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
			}
		}
		sort.Strings(ready)
	}

	inFlight := 0

	dispatch := func() {
		for len(ready) > 0 && inFlight < max && infraErr == nil {
			id := ready[0]
			ready = ready[1:]

			if reason, skip := needsSkip(g, id, results); skip {
				results[id] = &Result{ID: id, Status: StatusSkipped, Detail: reason}
				if opts.OnSkip != nil {
					opts.OnSkip(id, reason)
				}
				advance(id)
				continue
			}

			sem <- struct{}{}
			inFlight++
			jobID := id
			go func() {
				defer func() { <-sem }()
				res, err := opts.RunJob(ctx, jobID)
				done <- completion{id: jobID, res: res, err: err}
			}()
		}
	}

	dispatch()
	for inFlight > 0 {
		c := <-done
		inFlight--

		if c.err != nil {
			if infraErr == nil {
				infraErr = c.err
				cancel() // cancel any other in-flight jobs
			}
			// Record the job as skipped so it shows up in the result map.
			// The infra error itself is returned separately for the caller
			// to surface as an exit-3 condition.
			results[c.id] = &Result{ID: c.id, Status: StatusSkipped, Detail: "infra error"}
		} else {
			r := c.res
			r.ID = c.id // defensively normalize
			results[c.id] = &r
		}
		advance(c.id)
		dispatch()
	}

	// Backfill any never-launched jobs (after an infra error) as skipped.
	if infraErr != nil {
		for _, id := range g.Order {
			if results[id] == nil {
				results[id] = &Result{ID: id, Status: StatusSkipped, Detail: "infra error"}
				if opts.OnSkip != nil {
					opts.OnSkip(id, "infra error")
				}
			}
		}
	}

	return results, infraErr
}

// needsSkip reports whether a job must be skipped because one of its needs
// did not succeed. By the time it's called, every dependency already has a
// result (because the indegree only reaches 0 after every parent has
// completed and called advance).
func needsSkip(g *dag.Graph, id string, results map[string]*Result) (string, bool) {
	for _, need := range g.Needs[id] {
		if dep := results[need]; dep != nil && dep.Status != StatusSuccess {
			return fmt.Sprintf("%s %s", need, dep.Status), true
		}
	}
	return "", false
}
