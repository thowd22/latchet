package engine

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/dag"
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
	if _, err := loadAndValidate(opts); err != nil {
		return ExitConfig
	}
	return ExitSuccess
}

// DryRun loads, validates, and prints the workflow's execution plan as a
// sequence of parallel waves, then exits without contacting the container
// runtime. Exit codes: 0 on success, ExitConfig on parse/validation error.
func DryRun(opts Options) int {
	opts = opts.resolve()
	wf, err := loadAndValidate(opts)
	if err != nil {
		return ExitConfig
	}

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

// loadAndValidate parses the workflow file and runs Validate, reporting any
// error to opts.Stderr. Used by both Validate and DryRun.
func loadAndValidate(opts Options) (*config.Workflow, error) {
	wf, err := config.Load(opts.File)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return nil, err
	}
	if err := wf.Validate(); err != nil {
		fmt.Fprintf(opts.Stderr, "latchet: %v\n", err)
		return nil, err
	}
	return wf, nil
}
