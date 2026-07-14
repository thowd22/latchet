package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/dag"
	"github.com/thowd22/latchet/internal/keys"
)

// Options configures a workflow run. Future v2 fields (DryRun, MaxParallel)
// land in later steps; the struct exists now so callers can be migrated only
// once.
type Options struct {
	// File is the workflow file to load. Required.
	File string

	// DryRun, when true, prints the execution plan and returns without
	// running any containers. (Wired in step 5.)
	DryRun bool

	// MaxParallel caps concurrent jobs. 0 means "use the scheduler default."
	// (Wired in step 9.)
	MaxParallel int

	// Stdout receives streaming output and the summary. Defaults to os.Stdout.
	Stdout io.Writer

	// Stderr receives diagnostic messages. Defaults to os.Stderr.
	Stderr io.Writer

	// DefaultEnv holds machine-wide default env (from the global config),
	// merged below the workflow's own env so a workflow always overrides it.
	DefaultEnv map[string]string

	// GitRef, when set, is the full refname (refs/heads/… or refs/tags/…) that
	// triggered this run. It overrides the branch/tag the CWD git probe would
	// report — needed by `latchet watch`, which checks out a detached commit
	// the probe can't map back to a branch. Empty for ordinary runs.
	GitRef string

	// Functions holds machine-wide (global) functions from the global config;
	// a workflow's own `functions:` shadow these by name.
	Functions map[string]*config.Function
}

// overlayDefaultEnv returns wf.Env with defaults merged underneath: a key set
// by the workflow overrides the same key in defaults. Used to apply the global
// config's default env at workflow-env precedence.
func overlayDefaultEnv(defaults, wfEnv map[string]string) map[string]string {
	if len(defaults) == 0 {
		return wfEnv
	}
	merged := make(map[string]string, len(defaults)+len(wfEnv))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range wfEnv {
		merged[k] = v
	}
	return merged
}

// resolve fills in defaults for fields the caller left zero, returning a
// fully-populated Options safe to use throughout the run.
func (o Options) resolve() Options {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o
}

// Validate loads the workflow file and runs full validation, returning an
// exit code. It does not detect the runtime or allocate a workspace, so it
// is safe to invoke in environments without docker or podman.
func Validate(opts Options) int {
	opts = opts.resolve()
	if _, _, err := loadAndValidate(opts); err != nil {
		return exitFor(err)
	}
	return ExitSuccess
}

// DryRun loads, validates, and prints the workflow's execution plan as a
// sequence of parallel waves, then exits without contacting the container
// runtime. Exit codes: 0 on success, ExitConfig on parse/validation error.
func DryRun(opts Options) int {
	opts = opts.resolve()
	wf, _, err := loadAndValidate(opts)
	if err != nil {
		return exitFor(err)
	}
	wf = config.ExpandMatrix(wf) // show the expanded plan

	g, err := dag.Build(wf.Deps())
	if err != nil {
		// Unreachable after Validate, but report cleanly if it ever fires.
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return ExitConfig
	}

	name := wf.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(opts.Stdout, "latchet: workflow %s — dry-run\n", name)

	for i, wave := range dag.Waves(g) {
		fmt.Fprintf(opts.Stdout, "wave %d:\n", i+1)
		for _, id := range wave {
			job := wf.Jobs[id]
			line := fmt.Sprintf("image=%s  steps=%d", job.Container, len(job.Steps))
			if len(job.Needs) > 0 {
				line += fmt.Sprintf("  needs=[%s]", strings.Join([]string(job.Needs), ","))
			}
			fmt.Fprintf(opts.Stdout, "  %-12s %s\n", id, line)
		}
	}
	return ExitSuccess
}

// loadAndValidate parses the workflow file, resolves any `uses:` keys, and
// runs Validate, reporting any error to opts.Stderr. Also returns the
// resolved-keys map (verbatim uses string -> git+url[//subpath]@sha URI) for
// provenance. Used by Run, Validate, and DryRun.
func loadAndValidate(opts Options) (*config.Workflow, map[string]string, error) {
	wf, err := config.Load(opts.File)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return nil, nil, err
	}
	// Overlay global functions before validating, so a `call:` to a global
	// helper resolves; a workflow's own functions shadow globals by name.
	wf.Functions = config.MergeFunctions(opts.Functions, wf.Functions)
	// Fetch and resolve `uses:` keys before validating: checking a step's
	// `with:` needs the key's declared inputs. SHA-pinned keys hit the local
	// cache without touching the network.
	fns, resolved, err := keys.ResolveAll(context.Background(), wf, opts.Stdout)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return nil, nil, err
	}
	wf.Keys = fns
	if err := wf.Validate(); err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return nil, nil, err
	}
	return wf, resolved, nil
}

// exitFor maps a load/validate error to an exit code: fetch failures
// (git/network/cache IO) are infrastructure errors, everything else is a
// config error.
func exitFor(err error) int {
	var fe *keys.FetchError
	if errors.As(err, &fe) {
		return ExitInfra
	}
	return ExitConfig
}
