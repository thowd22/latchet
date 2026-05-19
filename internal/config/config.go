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

	"github.com/thowd22/latchet/internal/dag"
	"gopkg.in/yaml.v3"
)

// Workflow is a complete latchet.yml document.
type Workflow struct {
	Name string            `yaml:"name"`
	Env  map[string]string `yaml:"env"`
	Jobs map[string]*Job   `yaml:"jobs"`
}

// Job is one unit of work, executed inside a single container.
type Job struct {
	ID        string            `yaml:"-"` // filled from the jobs map key
	Container string            `yaml:"container"`
	Env       map[string]string `yaml:"env"`
	Needs     StringOrSlice     `yaml:"needs"`
	Steps     []*Step           `yaml:"steps"`
}

// Step is one shell command run inside its job's container.
type Step struct {
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
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
// typos and unsupported GitHub Actions features (uses, strategy, runs-on, ...)
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
		for i, step := range job.Steps {
			if step == nil || strings.TrimSpace(step.Run) == "" {
				errs = append(errs, fmt.Sprintf("job %q: step %d has an empty 'run'", id, i+1))
			}
		}
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
