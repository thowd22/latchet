// Package config defines the latchet.yml schema and loads/validates it.
//
// The schema deliberately mirrors a small subset of GitHub Actions: a
// workflow has a name and jobs; each job names a container image, optional
// env, optional dependencies (needs), and an ordered list of steps; each step
// has a name, a shell command (run), and optional env.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/thowd22/latchet/internal/cond"
	"github.com/thowd22/latchet/internal/dag"
	"gopkg.in/yaml.v3"
)

// Workflow is a complete latchet.yml document.
type Workflow struct {
	Name      string               `yaml:"name"`
	Env       map[string]string    `yaml:"env"`
	Jobs      map[string]*Job      `yaml:"jobs"`
	Functions map[string]*Function `yaml:"functions"` // reusable step sequences callable via `call:`
	// Keys holds resolved `uses:` keys (fetched functions), keyed by the
	// verbatim uses string. Not part of the YAML schema: the engine fetches
	// and populates it (internal/keys.ResolveAll) before Validate runs.
	Keys map[string]*Function `yaml:"-"`
	// Deterministic, when true, applies the determinism helpers to every job
	// (inject SOURCE_DATE_EPOCH, LC_ALL=C, LANG=C, TZ=UTC). A job may also set
	// it individually; LATCHET_DETERMINISTIC=1 forces it on globally.
	Deterministic bool `yaml:"deterministic"`
	// Secrets names host environment variables whose values are injected into
	// every job's steps and masked in logs and provenance. A job may declare
	// its own; the two lists are unioned per job.
	Secrets []string `yaml:"secrets"`
}

// Job is one unit of work, executed inside a single container.
type Job struct {
	ID            string            `yaml:"-"` // filled from the jobs map key
	Container     string            `yaml:"container"`
	Env           map[string]string `yaml:"env"`
	Needs         StringOrSlice     `yaml:"needs"`
	Inherit       string            `yaml:"inherit"` // name a single parent whose /workspace is copied in before this job runs; must also appear in needs
	Steps         []*Step           `yaml:"steps"`
	Deterministic bool              `yaml:"deterministic"` // apply determinism helpers to this job
	Secrets       []string          `yaml:"secrets"`       // host env var names injected + masked for this job
	Outputs       []string          `yaml:"outputs"`       // env var names (set via $LATCHET_ENV) exported to needs-dependents
	If            string            `yaml:"if"`            // condition; the whole job is skipped when false (cond syntax)
	Strategy      *Strategy         `yaml:"strategy"`      // fan this job across a matrix of variables (expanded before the run)
}

// Strategy configures how a job fans out. Matrix maps each variable name to its
// list of values; the job is expanded into one job per combination of values.
type Strategy struct {
	Matrix map[string][]string `yaml:"matrix"`
}

// Step is one shell command run inside its job's container. A step may carry a
// condition: `if:` starts a chain, `elif:` continues it, and `else: true` is
// the fallback. Within a chain the first branch whose condition is true runs;
// the rest are skipped. A plain step (no condition) ends any open chain.
//
// Alternatively a step may `call:` a function or `uses:` a key (a fetched
// function from a remote git repo) instead of `run:`-ning a command; the
// function's steps are inlined with the step's `with:` inputs.
type Step struct {
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	If   string            `yaml:"if"`   // condition; starts a conditional chain
	Elif string            `yaml:"elif"` // condition; continues the preceding if/elif chain
	Else bool              `yaml:"else"` // `else: true`; fallback branch of the chain
	Call string            `yaml:"call"` // name of a function to invoke instead of run
	Uses string            `yaml:"uses"` // key reference (url//subpath@ref) to invoke instead of run
	With map[string]string `yaml:"with"` // inputs passed to the called function or key
}

// Function is a reusable, parameterized sequence of steps invoked by `call:`.
// Declared in a workflow's `functions:` (local) or the global latchet-ci.yml
// (machine-wide); a local function shadows a global one of the same name.
type Function struct {
	Inputs map[string]*Input `yaml:"inputs"`
	Steps  []*Step           `yaml:"steps"`
}

// Input declares a function parameter.
type Input struct {
	Required bool   `yaml:"required"`
	Default  string `yaml:"default"`
}

// kind classifies a step's role in a conditional chain.
type stepKind int

const (
	stepPlain stepKind = iota
	stepIf
	stepElif
	stepElse
)

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

func (s *Step) kind() stepKind {
	switch {
	case s.If != "":
		return stepIf
	case s.Elif != "":
		return stepElif
	case s.Else:
		return stepElse
	default:
		return stepPlain
	}
}

// StringOrSlice accepts a YAML value that is either a single scalar or a
// sequence of scalars, so `needs: build` and `needs: [build, test]` both work.
type StringOrSlice []string

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *StringOrSlice) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		*s = StringOrSlice{single}
		return nil
	}
	var multi []string
	if err := value.Decode(&multi); err != nil {
		return err
	}
	*s = multi
	return nil
}

// Load reads and parses a latchet.yml file. Unknown keys are rejected so that
// typos and unsupported GitHub Actions features (runs-on, timeout-minutes, ...)
// fail loudly instead of being silently ignored.
func Load(path string) (*Workflow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var wf Workflow
	if err := dec.Decode(&wf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Copy each map key into Job.ID; replace nil jobs (e.g. `build:` with no
	// body) with empty ones so validation can report them cleanly.
	for id := range wf.Jobs {
		if wf.Jobs[id] == nil {
			wf.Jobs[id] = &Job{}
		}
		wf.Jobs[id].ID = id
	}
	return &wf, nil
}

// Deps returns the dependency graph in the shape dag.Sort expects: each job ID
// mapped to the IDs of the jobs it needs.
func (wf *Workflow) Deps() map[string][]string {
	deps := make(map[string][]string, len(wf.Jobs))
	for id, job := range wf.Jobs {
		deps[id] = []string(job.Needs)
	}
	return deps
}

// Validate checks the whole workflow and returns every problem found, not just
// the first, joined into a single error.
func (wf *Workflow) Validate() error {
	var errs []string

	if len(wf.Jobs) == 0 {
		errs = append(errs, "no jobs defined")
	}

	ids := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic error ordering

	needsSane := true
	for _, id := range ids {
		job := wf.Jobs[id]
		if strings.TrimSpace(job.Container) == "" {
			errs = append(errs, fmt.Sprintf("job %q: missing 'container'", id))
		}
		if len(job.Steps) == 0 {
			errs = append(errs, fmt.Sprintf("job %q: has no steps", id))
		}
		for _, name := range job.Outputs {
			if !validEnvName(name) {
				errs = append(errs, fmt.Sprintf("job %q: output %q is not a valid env var name", id, name))
			}
		}
		if job.If != "" {
			if err := cond.Check(job.If); err != nil {
				errs = append(errs, fmt.Sprintf("job %q: if: %v", id, err))
			}
		}
		if job.Strategy != nil {
			if len(job.Strategy.Matrix) == 0 {
				errs = append(errs, fmt.Sprintf("job %q: strategy.matrix is empty", id))
			}
			mkeys := make([]string, 0, len(job.Strategy.Matrix))
			for k := range job.Strategy.Matrix {
				mkeys = append(mkeys, k)
			}
			sort.Strings(mkeys)
			for _, k := range mkeys {
				if !validEnvName(k) {
					errs = append(errs, fmt.Sprintf("job %q: matrix key %q is not a valid env var name", id, k))
				}
				if len(job.Strategy.Matrix[k]) == 0 {
					errs = append(errs, fmt.Sprintf("job %q: matrix %q has no values", id, k))
				}
			}
		}
		// Inheriting from a matrix job is ambiguous (which expansion?).
		if job.Inherit != "" {
			if p := wf.Jobs[job.Inherit]; p != nil && p.Strategy != nil {
				errs = append(errs, fmt.Sprintf("job %q: cannot inherit from matrix job %q", id, job.Inherit))
			}
		}
		errs = append(errs, validateSteps(fmt.Sprintf("job %q", id), job.Steps, wf.Functions, wf.Keys, true)...)
		for _, need := range job.Needs {
			switch {
			case need == id:
				errs = append(errs, fmt.Sprintf("job %q: cannot depend on itself", id))
				needsSane = false
			case wf.Jobs[need] == nil:
				errs = append(errs, fmt.Sprintf("job %q: needs unknown job %q", id, need))
				needsSane = false
			}
		}
		if job.Inherit != "" {
			switch {
			case job.Inherit == id:
				errs = append(errs, fmt.Sprintf("job %q: cannot inherit from itself", id))
			case wf.Jobs[job.Inherit] == nil:
				errs = append(errs, fmt.Sprintf("job %q: inherits unknown job %q", id, job.Inherit))
			default:
				inNeeds := false
				for _, need := range job.Needs {
					if need == job.Inherit {
						inNeeds = true
						break
					}
				}
				if !inNeeds {
					errs = append(errs, fmt.Sprintf("job %q: inherits from %q but does not list it in needs", id, job.Inherit))
				}
			}
		}
	}

	// Validate function definitions (deterministic order). Function bodies may
	// not themselves `call:` or `uses:` (no nesting).
	fnames := make([]string, 0, len(wf.Functions))
	for name := range wf.Functions {
		fnames = append(fnames, name)
	}
	sort.Strings(fnames)
	for _, name := range fnames {
		fn := wf.Functions[name]
		if len(fn.Steps) == 0 {
			errs = append(errs, fmt.Sprintf("function %q: has no steps", name))
		}
		for in := range fn.Inputs {
			if !validEnvName(in) {
				errs = append(errs, fmt.Sprintf("function %q: input %q is not a valid env var name", name, in))
			}
		}
		errs = append(errs, validateSteps(fmt.Sprintf("function %q", name), fn.Steps, wf.Functions, nil, false)...)
	}

	// A cycle check only makes sense once every `needs` edge points somewhere
	// real; otherwise dag.Sort would just re-report the dangling references.
	if needsSane {
		if _, err := dag.Sort(wf.Deps()); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid workflow:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
