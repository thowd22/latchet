// Package engine orchestrates a workflow run: it loads and validates the
// config, builds the dependency graph, and hands every job to the
// scheduler. Each job runs inside its own container; the scheduler enforces
// dependency order and concurrency limits.
package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thowd22/latchet/internal/builtinenv"
	"github.com/thowd22/latchet/internal/cond"
	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/dag"
	"github.com/thowd22/latchet/internal/envutil"
	"github.com/thowd22/latchet/internal/log"
	"github.com/thowd22/latchet/internal/logstore"
	"github.com/thowd22/latchet/internal/mask"
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
	wf.Env = overlayDefaultEnv(opts.DefaultEnv, wf.Env)
	wf = config.ExpandMatrix(wf) // fan out strategy.matrix jobs before the DAG

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
	if opts.GitRef != "" {
		// A trigger (e.g. `latchet watch`) knows the ref a detached checkout
		// can't report; let it set the branch/tag/ref.
		git = builtinenv.OverrideRef(git, opts.GitRef)
	}
	// SOURCE_DATE_EPOCH (used by the determinism helpers) falls back to the
	// run-start time when HEAD's commit time is unavailable. Fixed once per run
	// so every job sees the same value.
	if git.CommitEpoch == "" {
		git.CommitEpoch = strconv.FormatInt(time.Now().Unix(), 10)
	}

	jobOuts := newJobOutputs()
	started := time.Now()
	results, infraErr := scheduler.Run(context.Background(), g, scheduler.Options{
		MaxParallel: maxParallel,
		RunJob: func(ctx context.Context, jobID string) (scheduler.Result, error) {
			return runOne(ctx, rt, ws, ls, wf, jobID, images, opts.Stdout, maxParallel, git, jobOuts)
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
	emitProvenance(context.Background(), ws, ls, wf, opts, git, images, jobOuts, maxParallel, started, finished, opts.Stdout, opts.Stderr)

	if kept := ws.Cleanup(exit != ExitSuccess); kept != "" {
		fmt.Fprintf(opts.Stdout, "\nworkspace kept at %s\n", kept)
	}
	return exit
}

// jobOutputs is the run-wide store of each job's exported outputs, read by
// dependents. The scheduler runs a job only after its needs have completed, so
// a dependent always sees its needs' outputs; the mutex guards concurrent
// writes by jobs running in the same wave.
type jobOutputs struct {
	mu sync.Mutex
	m  map[string]map[string]string
}

func newJobOutputs() *jobOutputs { return &jobOutputs{m: map[string]map[string]string{}} }

func (j *jobOutputs) set(id string, out map[string]string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.m[id] = out
}

// needsEnv merges the outputs of the given dependency jobs into one env map
// (later needs win on a name clash).
func (j *jobOutputs) needsEnv(needs []string) map[string]string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]string{}
	for _, n := range needs {
		for k, v := range j.m[n] {
			out[k] = v
		}
	}
	return out
}

// runOne wraps runJob with the per-job log file setup. The log file always
// records the full output; for maxParallel == 1 it is teed to stdout so the
// user sees streaming output (matching v1's UX). For maxParallel > 1, stdout
// gets only terse begin/end markers, since interleaving live step output
// from concurrent jobs is unreadable.
func runOne(ctx context.Context, rt *runtime.Runtime, ws *workspace.Run, ls *logstore.Run, wf *config.Workflow, jobID string, images *imageCache, stdout io.Writer, maxParallel int, git builtinenv.Git, outs *jobOutputs) (scheduler.Result, error) {
	job := wf.Jobs[jobID]
	// Outputs declared by this job's dependencies are injected as env vars.
	needsEnv := outs.needsEnv([]string(job.Needs))

	// Job-level condition: skip the whole job when false, before any log/setup.
	// A skipped job propagates to its dependents via the scheduler (needs-skip).
	if job.If != "" {
		evalEnv := mergeEnv(jobBuiltins(ws.ID, job, wf, git), needsEnv, wf.Env, job.Env, resolveSecrets(wf, job))
		ok, cerr := cond.Eval(job.If, evalEnv)
		if cerr != nil {
			return scheduler.Result{ID: jobID}, fmt.Errorf("job %q if: %w", jobID, cerr)
		}
		if !ok {
			if maxParallel > 1 {
				fmt.Fprintf(stdout, "\n== job: %s -> skipped (if condition false) ==\n", jobID)
			} else {
				log.JobSkip(stdout, jobID, "if condition false")
			}
			return scheduler.Result{ID: jobID, Status: scheduler.StatusSkipped, Detail: "if condition false"}, nil
		}
	}

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

	// Mask declared secret values in everything this job writes (log file and,
	// in serial mode, stdout). Passthrough when the job declares no secrets.
	mw := mask.New(stepW, secretValues(resolveSecrets(wf, job)))

	res, exported, err := runJob(ctx, rt, ws, wf, job, images, mw, git, needsEnv)
	mw.Close() // flush any masked tail held back across writes
	if err == nil && res.Status == scheduler.StatusSuccess {
		outs.set(jobID, exported)
	}
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

// jobDeterministic reports whether the determinism helpers apply to this job:
// set workflow-wide, on the job, or forced via LATCHET_DETERMINISTIC=1.
func jobDeterministic(wf *config.Workflow, job *config.Job) bool {
	return wf.Deterministic || job.Deterministic || os.Getenv("LATCHET_DETERMINISTIC") == "1"
}

// jobBuiltins builds the lowest-precedence env base for a job: the LATCHET_*
// built-ins plus, when the job is deterministic, the determinism helpers
// (SOURCE_DATE_EPOCH, LC_ALL, LANG, TZ). Used by both execution and provenance
// so the recorded env matches what ran.
func jobBuiltins(runID string, job *config.Job, wf *config.Workflow, git builtinenv.Git) map[string]string {
	m := builtinenv.For(runID, job.ID, "/workspace", git)
	if jobDeterministic(wf, job) {
		for k, v := range builtinenv.Deterministic(git) {
			m[k] = v
		}
	}
	return m
}

// resolveSecrets reads the host environment for every secret name declared on
// the workflow or this job, returning name->value for those that are set and
// non-empty. These are injected into the job's steps and masked in output.
func resolveSecrets(wf *config.Workflow, job *config.Job) map[string]string {
	names := make(map[string]bool, len(wf.Secrets)+len(job.Secrets))
	for _, n := range wf.Secrets {
		names[n] = true
	}
	for _, n := range job.Secrets {
		names[n] = true
	}
	out := map[string]string{}
	for n := range names {
		if v := os.Getenv(n); v != "" {
			out[n] = v
		}
	}
	return out
}

// stepShouldRun applies a step's if/elif/else condition against the merged env,
// updating the chain state. A plain step always runs and ends any open chain;
// within an if/elif/else chain the first branch whose condition is true runs
// and the rest are skipped. Returns (run, skipReason, evalError).
func stepShouldRun(step *config.Step, env map[string]string, chainTaken *bool) (bool, string, error) {
	switch {
	case step.If != "":
		*chainTaken = false
		ok, err := cond.Eval(step.If, env)
		if err != nil {
			return false, "", fmt.Errorf("if: %w", err)
		}
		if ok {
			*chainTaken = true
			return true, "", nil
		}
		return false, "if condition false", nil
	case step.Elif != "":
		if *chainTaken {
			return false, "an earlier branch ran", nil
		}
		ok, err := cond.Eval(step.Elif, env)
		if err != nil {
			return false, "", fmt.Errorf("elif: %w", err)
		}
		if ok {
			*chainTaken = true
			return true, "", nil
		}
		return false, "elif condition false", nil
	case step.Else:
		if *chainTaken {
			return false, "an earlier branch ran", nil
		}
		*chainTaken = true
		return true, "", nil
	default:
		*chainTaken = false
		return true, "", nil
	}
}

// containerName builds the runtime container name for a job. The job ID can
// contain characters a matrix expansion introduces (spaces, parens, "=") that
// docker/podman reject in names, so it is sanitized to [A-Za-z0-9_.-]; the
// substitution is position-preserving, so distinct job IDs stay distinct.
func containerName(runID, jobID string) string {
	var b strings.Builder
	b.WriteString("latchet-")
	b.WriteString(runID)
	b.WriteByte('-')
	for _, r := range jobID {
		switch {
		case r == '_' || r == '.' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// readEnvFile parses a $LATCHET_ENV file into name->value. Each non-blank line
// is `NAME=value` (value may contain `=`); lines without `=` or with an invalid
// env-var name are ignored. A missing file yields no outputs and no error.
func readEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); validEnvName(k) {
			out[k] = v
		}
	}
	return out, nil
}

// validEnvName reports whether s is a POSIX-style env var name
// ([A-Za-z_][A-Za-z0-9_]*).
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// secretValues returns just the values of a resolved-secrets map.
func secretValues(m map[string]string) []string {
	vals := make([]string, 0, len(m))
	for _, v := range m {
		vals = append(vals, v)
	}
	return vals
}

// runJob executes one job inside a freshly created container. A non-nil error
// signals an infrastructure failure that the scheduler should treat as
// aborting; a step exiting non-zero is reported as a scheduler.Result with
// StatusFailed.
func runJob(ctx context.Context, rt *runtime.Runtime, ws *workspace.Run, wf *config.Workflow, job *config.Job, images *imageCache, out io.Writer, git builtinenv.Git, needsEnv map[string]string) (scheduler.Result, map[string]string, error) {
	log.JobStart(out, job.ID)

	// Built-in vars are identical for every step in the job and form the
	// lowest-precedence base of the env merge, so user env can override them.
	// "/workspace" is the fixed container-side mount point (see runtime).
	builtins := jobBuiltins(ws.ID, job, wf, git)

	// Declared secrets are pulled from the host env and injected just below
	// step env. Their values are masked in this job's output by runOne.
	secretEnv := resolveSecrets(wf, job)

	// Inline any `call:` steps: replace each with the called function's steps,
	// with the call's `with:` inputs expanded against the job's static env
	// (everything known before steps run — not step outputs).
	staticBase := mergeEnv(builtins, needsEnv, wf.Env, job.Env, secretEnv)
	steps := config.ExpandCalls(job.Steps, wf.Functions, func(v string) string {
		return config.ExpandVars(v, staticBase)
	})

	jobDir, err := ws.JobDir(job.ID)
	if err != nil {
		return scheduler.Result{ID: job.ID}, nil, err
	}

	if job.Inherit != "" {
		if err := ws.Seed(job.ID, job.Inherit); err != nil {
			return scheduler.Result{ID: job.ID}, nil, err
		}
	}

	if err := images.Ensure(ctx, rt, job.Container, out); err != nil {
		return scheduler.Result{ID: job.ID}, nil, err
	}

	container := containerName(ws.ID, job.ID)
	if err := rt.Create(ctx, container, job.Container, jobDir); err != nil {
		return scheduler.Result{ID: job.ID}, nil, err
	}
	defer rt.Remove(container)

	// Step outputs: a step appends NAME=value to $LATCHET_ENV (host-readable at
	// jobDir/.latchet/env via the workspace mount); latchet merges them into
	// later steps' env. Start clean so an inherited workspace's file can't leak
	// a parent job's outputs.
	metaDir := filepath.Join(jobDir, ".latchet")
	_ = os.RemoveAll(metaDir)
	if err := os.MkdirAll(metaDir, 0o777); err != nil {
		return scheduler.Result{ID: job.ID}, nil, err
	}
	_ = os.Chmod(metaDir, 0o777) // the container user may differ from latchet's
	envFile := filepath.Join(metaDir, "env")
	outputs := map[string]string{} // accumulated NAME=value step outputs

	chainTaken := false // an if/elif chain has already taken a branch
	for i, step := range steps {
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step %d", i+1)
		}

		// Conditions and the step itself see the full merged env: built-ins,
		// dependency (needs) outputs, workflow/job env, secrets, this job's own
		// accumulated step outputs, then the step's own env (highest).
		merged := mergeEnv(builtins, needsEnv, wf.Env, job.Env, secretEnv, outputs, step.Env)
		run, skipReason, err := stepShouldRun(step, merged, &chainTaken)
		if err != nil {
			return scheduler.Result{ID: job.ID}, nil, fmt.Errorf("job %q %s: %w", job.ID, name, err)
		}
		if !run {
			log.StepSkip(out, name, skipReason)
			continue
		}
		log.StepStart(out, name)

		env := envutil.Merge(merged)
		start := time.Now()
		code, err := rt.Exec(ctx, container, env, step.Run, out, out)
		if err != nil {
			return scheduler.Result{ID: job.ID}, nil, err
		}
		if code != 0 {
			log.StepEnd(out, name, false, time.Since(start))
			detail := fmt.Sprintf("%s exited %d", name, code)
			log.JobEnd(out, job.ID, string(scheduler.StatusFailed), detail)
			return scheduler.Result{ID: job.ID, Status: scheduler.StatusFailed, Detail: detail}, nil, nil
		}
		// Pick up any NAME=value lines the step appended to $LATCHET_ENV.
		if set, rerr := readEnvFile(envFile); rerr != nil {
			fmt.Fprintf(out, "-- step: %s -> LATCHET_ENV unreadable: %v --\n", name, rerr)
		} else {
			for k, v := range set {
				outputs[k] = v
			}
		}
		log.StepEnd(out, name, true, time.Since(start))
	}

	log.JobEnd(out, job.ID, string(scheduler.StatusSuccess), "")
	return scheduler.Result{ID: job.ID, Status: scheduler.StatusSuccess}, exportedOutputs(job, outputs, out), nil
}

// exportedOutputs selects the job's declared outputs from its accumulated step
// outputs. A declared name that was never set is exported as "" with a warning,
// so dependents see a stable key set.
func exportedOutputs(job *config.Job, outputs map[string]string, warn io.Writer) map[string]string {
	if len(job.Outputs) == 0 {
		return nil
	}
	out := make(map[string]string, len(job.Outputs))
	for _, name := range job.Outputs {
		v, ok := outputs[name]
		if !ok {
			fmt.Fprintf(warn, "-- job %s: declared output %q was never set --\n", job.ID, name)
		}
		out[name] = v
	}
	return out
}
