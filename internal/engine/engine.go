// Package engine orchestrates a workflow run: it loads and validates the
// config, orders jobs by their dependencies, executes each job inside a
// container, and reports results.
package engine

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/dag"
	"github.com/thowd22/latchet/internal/envutil"
	"github.com/thowd22/latchet/internal/log"
	"github.com/thowd22/latchet/internal/runtime"
	"github.com/thowd22/latchet/internal/workspace"
)

// Process exit codes returned by Run.
const (
	ExitSuccess = 0 // every job succeeded
	ExitFailed  = 1 // at least one job failed
	ExitConfig  = 2 // the workflow file is missing or invalid
	ExitInfra   = 3 // a container/runtime/workspace operation failed
)

// Run executes the workflow at workflowPath and returns a process exit code.
func Run(workflowPath string) int {
	wf, err := config.Load(workflowPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "latchet: %v\n", err)
		return ExitConfig
	}
	if err := wf.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "latchet: %v\n", err)
		return ExitConfig
	}

	rt, err := runtime.Detect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "latchet: %v\n", err)
		return ExitInfra
	}

	// Validate already proved the graph is acyclic, so this cannot fail.
	order, err := dag.Sort(wf.Deps())
	if err != nil {
		fmt.Fprintf(os.Stderr, "latchet: %v\n", err)
		return ExitConfig
	}

	ws, err := workspace.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "latchet: %v\n", err)
		return ExitInfra
	}

	ctx := context.Background()
	out := os.Stdout

	name := wf.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(out, "latchet: workflow %s — using %s — run %s\n", name, rt.Bin, ws.ID)

	results := make(map[string]*JobResult, len(order))
	pulled := make(map[string]bool)

	for _, id := range order {
		job := wf.Jobs[id]

		if reason, skip := skipReason(job, results); skip {
			results[id] = &JobResult{ID: id, Status: StatusSkipped, Detail: reason}
			log.JobSkip(out, id, reason)
			continue
		}

		res, infraErr := runJob(ctx, rt, ws, wf, job, pulled)
		if infraErr != nil {
			fmt.Fprintf(os.Stderr, "latchet: %v\n", infraErr)
			if kept := ws.Cleanup(true); kept != "" {
				fmt.Fprintf(os.Stderr, "latchet: workspace kept at %s\n", kept)
			}
			return ExitInfra
		}
		results[id] = res
	}

	exit := ExitSuccess
	for _, r := range results {
		if r.Status == StatusFailed {
			exit = ExitFailed
			break
		}
	}

	log.SummaryHeader(out)
	for _, id := range order {
		log.SummaryLine(out, id, string(results[id].Status))
	}

	if kept := ws.Cleanup(exit != ExitSuccess); kept != "" {
		fmt.Fprintf(out, "\nworkspace kept at %s\n", kept)
	}
	return exit
}

// skipReason reports whether a job must be skipped because one of the jobs it
// needs did not succeed. Topological ordering guarantees every dependency has
// a recorded result by the time this is called.
func skipReason(job *config.Job, results map[string]*JobResult) (string, bool) {
	for _, need := range job.Needs {
		if dep := results[need]; dep != nil && dep.Status != StatusSuccess {
			return fmt.Sprintf("%s %s", need, dep.Status), true
		}
	}
	return "", false
}

// runJob executes one job inside a freshly created container. A non-nil error
// signals an infrastructure failure that should abort the whole run; a step
// exiting non-zero is reported through JobResult instead.
func runJob(ctx context.Context, rt *runtime.Runtime, ws *workspace.Run, wf *config.Workflow, job *config.Job, pulled map[string]bool) (*JobResult, error) {
	out := os.Stdout
	log.JobStart(out, job.ID)

	jobDir, err := ws.JobDir(job.ID)
	if err != nil {
		return nil, err
	}

	if !pulled[job.Container] && !rt.ImageExists(ctx, job.Container) {
		fmt.Fprintf(out, "pulling image %s ...\n", job.Container)
		if err := rt.Pull(ctx, job.Container, out); err != nil {
			return nil, err
		}
	}
	pulled[job.Container] = true

	container := fmt.Sprintf("latchet-%s-%s", ws.ID, job.ID)
	if err := rt.Create(ctx, container, job.Container, jobDir); err != nil {
		return nil, err
	}
	defer rt.Remove(container)

	for i, step := range job.Steps {
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step %d", i+1)
		}
		log.StepStart(out, name)

		env := envutil.Merge(wf.Env, job.Env, step.Env)
		start := time.Now()
		code, err := rt.Exec(ctx, container, env, step.Run, out, out)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			log.StepEnd(out, name, false, time.Since(start))
			detail := fmt.Sprintf("%s exited %d", name, code)
			log.JobEnd(out, job.ID, string(StatusFailed), detail)
			return &JobResult{ID: job.ID, Status: StatusFailed, Detail: detail}, nil
		}
		log.StepEnd(out, name, true, time.Since(start))
	}

	log.JobEnd(out, job.ID, string(StatusSuccess), "")
	return &JobResult{ID: job.ID, Status: StatusSuccess}, nil
}
