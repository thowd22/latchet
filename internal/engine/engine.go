// Package engine orchestrates a workflow run: it loads and validates the
// config, builds the dependency graph, and hands every job to the
// scheduler. Each job runs inside its own container; the scheduler enforces
// dependency order and concurrency limits.
package engine

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/thowd22/latchet/internal/builtinenv"
	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/dag"
	"github.com/thowd22/latchet/internal/envutil"
	"github.com/thowd22/latchet/internal/log"
	"github.com/thowd22/latchet/internal/logstore"
	"github.com/thowd22/latchet/internal/runtime"
	"github.com/thowd22/latchet/internal/scheduler"
	"github.com/thowd22/latchet/internal/workspace"
)

// Process exit codes returned by Run.
const (
	ExitSuccess = 0 // every job succeeded
	ExitFailed  = 1 // at least one job failed
	ExitConfig  = 2 // the workflow file is missing or invalid
	ExitInfra   = 3 // a container/runtime/workspace operation failed
)

// Run executes the workflow described by opts and returns a process exit code.
func Run(opts Options) int {
	opts = opts.resolve()

	wf, err := loadAndValidate(opts)
	if err != nil {
		return ExitConfig
	}

	rt, err := runtime.Detect()
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return ExitInfra
	}

	// Validate already proved the graph is acyclic, so this cannot fail.
	g, err := dag.Build(wf.Deps())
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return ExitConfig
	}

	ws, err := workspace.New()
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return ExitInfra
	}

	ls, err := logstore.New(ws.ID)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return ExitInfra
	}

	name := wf.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(opts.Stdout, "latchet: workflow %s — using %s — run %s\n", name, rt.Bin, ws.ID)
	fmt.Fprintf(opts.Stdout, "latchet: logs at %s\n", ls.Dir)

	images := newImageCache()
	maxParallel := opts.MaxParallel
	if maxParallel < 1 {
		maxParallel = 1
	}

	// Resolve source-control facts once per run (run-level, host CWD) and inject
	// them into every job as LATCHET_GIT_* built-in env vars.
	git := builtinenv.ResolveGit(context.Background())

	started := time.Now()
	results, infraErr := scheduler.Run(context.Background(), g, scheduler.Options{
		MaxParallel: maxParallel,
		RunJob: func(ctx context.Context, jobID string) (scheduler.Result, error) {
			return runOne(ctx, rt, ws, ls, wf, jobID, images, opts.Stdout, maxParallel, git)
		},
		OnSkip: func(jobID, reason string) {
			log.JobSkip(opts.Stdout, jobID, reason)
		},
	})
	finished := time.Now()

	if infraErr != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", infraErr)
		if kept := ws.Cleanup(true); kept != "" {
			fmt.Fprintf(opts.Stderr, "latchet: workspace kept at %s\n", kept)
		}
		return ExitInfra
	}

	exit := ExitSuccess
	for _, r := range results {
		if r.Status == scheduler.StatusFailed {
			exit = ExitFailed
			break
		}
	}

	log.SummaryHeader(opts.Stdout)
	for _, id := range g.Order {
		log.SummaryLine(opts.Stdout, id, string(results[id].Status))
	}

	// Emit SLSA provenance before cleanup, while job artifacts still exist.
	emitProvenance(ws, ls, wf, opts, git, images, maxParallel, started, finished, opts.Stdout, opts.Stderr)

	if kept := ws.Cleanup(exit != ExitSuccess); kept != "" {
		fmt.Fprintf(opts.Stdout, "\nworkspace kept at %s\n", kept)
	}
	return exit
}

// runOne wraps runJob with the per-job log file setup. The log file always
// records the full output; for maxParallel == 1 it is teed to stdout so the
// user sees streaming output (matching v1's UX). For maxParallel > 1, stdout
// gets only terse begin/end markers, since interleaving live step output
// from concurrent jobs is unreadable.
func runOne(ctx context.Context, rt *runtime.Runtime, ws *workspace.Run, ls *logstore.Run, wf *config.Workflow, jobID string, images *imageCache, stdout io.Writer, maxParallel int, git builtinenv.Git) (scheduler.Result, error) {
	logFile, logPath, err := ls.OpenJob(jobID)
	if err != nil {
		return scheduler.Result{ID: jobID}, err
	}
	defer logFile.Close()

	var stepW io.Writer = logFile
	if maxParallel == 1 {
		stepW = io.MultiWriter(logFile, stdout)
	} else {
		fmt.Fprintf(stdout, "\n== job: %s started (log: %s) ==\n", jobID, logPath)
	}

	res, err := runJob(ctx, rt, ws, wf, wf.Jobs[jobID], images, stepW, git)
	if maxParallel > 1 {
		switch {
		case err != nil:
			fmt.Fprintf(stdout, "== job: %s -> infra error ==\n", jobID)
		case res.Status == scheduler.StatusFailed:
			fmt.Fprintf(stdout, "== job: %s -> failed (%s) ==\n", jobID, res.Detail)
		default:
			fmt.Fprintf(stdout, "== job: %s -> %s ==\n", jobID, res.Status)
		}
	}
	return res, err
}

// runJob executes one job inside a freshly created container. A non-nil error
// signals an infrastructure failure that the scheduler should treat as
// aborting; a step exiting non-zero is reported as a scheduler.Result with
// StatusFailed.
func runJob(ctx context.Context, rt *runtime.Runtime, ws *workspace.Run, wf *config.Workflow, job *config.Job, images *imageCache, out io.Writer, git builtinenv.Git) (scheduler.Result, error) {
	log.JobStart(out, job.ID)

	// Built-in vars are identical for every step in the job and form the
	// lowest-precedence base of the env merge, so user env can override them.
	// "/workspace" is the fixed container-side mount point (see runtime).
	builtins := builtinenv.For(ws.ID, job.ID, "/workspace", git)

	jobDir, err := ws.JobDir(job.ID)
	if err != nil {
		return scheduler.Result{ID: job.ID}, err
	}

	if job.Inherit != "" {
		if err := ws.Seed(job.ID, job.Inherit); err != nil {
			return scheduler.Result{ID: job.ID}, err
		}
	}

	if err := images.Ensure(ctx, rt, job.Container, out); err != nil {
		return scheduler.Result{ID: job.ID}, err
	}

	container := fmt.Sprintf("latchet-%s-%s", ws.ID, job.ID)
	if err := rt.Create(ctx, container, job.Container, jobDir); err != nil {
		return scheduler.Result{ID: job.ID}, err
	}
	defer rt.Remove(container)

	for i, step := range job.Steps {
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step %d", i+1)
		}
		log.StepStart(out, name)

		env := envutil.Merge(builtins, wf.Env, job.Env, step.Env)
		start := time.Now()
		code, err := rt.Exec(ctx, container, env, step.Run, out, out)
		if err != nil {
			return scheduler.Result{ID: job.ID}, err
		}
		if code != 0 {
			log.StepEnd(out, name, false, time.Since(start))
			detail := fmt.Sprintf("%s exited %d", name, code)
			log.JobEnd(out, job.ID, string(scheduler.StatusFailed), detail)
			return scheduler.Result{ID: job.ID, Status: scheduler.StatusFailed, Detail: detail}, nil
		}
		log.StepEnd(out, name, true, time.Since(start))
	}

	log.JobEnd(out, job.ID, string(scheduler.StatusSuccess), "")
	return scheduler.Result{ID: job.ID, Status: scheduler.StatusSuccess}, nil
}
