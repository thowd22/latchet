// Package provenance builds and writes a SLSA v1.0 provenance attestation for
// a latchet run, wrapped in an in-toto statement. Emitting it after every run
// brings the run to SLSA Build L1 with no user action.
//
// The structs are hand-rolled and marshaled with encoding/json so the package
// adds no third-party dependency. Build is deterministic: subjects, resolved
// dependencies, and jobs are sorted, so the same inputs always produce
// byte-identical JSON (important for a verifier comparing manifests).
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

// Canonical type URIs.
const (
	StatementType = "https://in-toto.io/Statement/v1"
	PredicateType = "https://slsa.dev/provenance/v1"
	BuildType     = "https://latchet.dev/buildtypes/v1"
	builderPrefix = "https://latchet.dev/builders/latchet"
	FileName      = "provenance.json"
)

// Statement is the in-toto envelope carrying the SLSA predicate.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

// Subject is one attested artifact: a name and its content digest.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type Predicate struct {
	BuildDefinition BuildDefinition `json:"buildDefinition"`
	RunDetails      RunDetails      `json:"runDetails"`
}

type BuildDefinition struct {
	BuildType            string         `json:"buildType"`
	ExternalParameters   ExternalParams `json:"externalParameters"`
	InternalParameters   InternalParams `json:"internalParameters"`
	ResolvedDependencies []Dependency   `json:"resolvedDependencies"`
}

type ExternalParams struct {
	Workflow   WorkflowRef       `json:"workflow"`
	Invocation map[string]string `json:"invocation"`
	Source     *SourceRef        `json:"source,omitempty"`
}

type WorkflowRef struct {
	Path   string            `json:"path"`
	Digest map[string]string `json:"digest"`
}

// SourceRef records the cause-of-build git facts (origin URL, ref, revision).
type SourceRef struct {
	URI      string `json:"uri,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type InternalParams struct {
	Jobs []JobParams `json:"jobs"`
}

type JobParams struct {
	ID    string       `json:"id"`
	Image string       `json:"image"`
	Steps []StepParams `json:"steps"`
}

type StepParams struct {
	Name string            `json:"name,omitempty"`
	Run  string            `json:"run"`
	Env  map[string]string `json:"env,omitempty"`
}

// Dependency is a build input pinned to a resolved digest (here, container
// images resolved at pull time).
type Dependency struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type RunDetails struct {
	Builder  Builder  `json:"builder"`
	Metadata Metadata `json:"metadata"`
}

type Builder struct {
	ID string `json:"id"`
}

type Metadata struct {
	InvocationID string `json:"invocationId"`
	StartedOn    string `json:"startedOn"`
	FinishedOn   string `json:"finishedOn"`
}

// Input is everything Build needs, in primitive form, so the package stays
// decoupled from the engine's types.
type Input struct {
	RunID          string
	Started        time.Time
	Finished       time.Time
	BuilderVersion string
	BuilderCommit  string

	WorkflowPath string
	WorkflowSHA  string            // hex sha256 of the workflow file bytes
	Invocation   map[string]string // file, max_parallel, dry_run, ...
	Source       *SourceRef

	Jobs     []JobParams       // internalParameters; sorted by ID in Build
	Images   map[string]string // image ref as written -> resolved @sha256 digest
	Subjects []Subject         // hashed artifacts; sorted in Build
}

// Build assembles a deterministic SLSA v1.0 statement from in.
func Build(in Input) Statement {
	subjects := append([]Subject(nil), in.Subjects...)
	sort.Slice(subjects, func(i, j int) bool { return subjects[i].Name < subjects[j].Name })
	// A statement must have at least one subject; when a run produced no file
	// artifacts (effect-only jobs), attest the workflow file itself.
	if len(subjects) == 0 && in.WorkflowSHA != "" {
		subjects = []Subject{{
			Name:   path.Base(filepath.ToSlash(in.WorkflowPath)),
			Digest: map[string]string{"sha256": in.WorkflowSHA},
		}}
	}

	deps := make([]Dependency, 0, len(in.Images))
	for ref, digest := range in.Images {
		deps = append(deps, Dependency{URI: digest, Name: ref})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Name < deps[j].Name })

	jobs := append([]JobParams(nil), in.Jobs...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })

	return Statement{
		Type:          StatementType,
		Subject:       subjects,
		PredicateType: PredicateType,
		Predicate: Predicate{
			BuildDefinition: BuildDefinition{
				BuildType: BuildType,
				ExternalParameters: ExternalParams{
					Workflow: WorkflowRef{
						Path:   in.WorkflowPath,
						Digest: map[string]string{"sha256": in.WorkflowSHA},
					},
					Invocation: in.Invocation,
					Source:     in.Source,
				},
				InternalParameters:   InternalParams{Jobs: jobs},
				ResolvedDependencies: deps,
			},
			RunDetails: RunDetails{
				Builder:  Builder{ID: builderID(in.BuilderVersion, in.BuilderCommit)},
				Metadata: Metadata{
					InvocationID: in.RunID,
					StartedOn:    in.Started.UTC().Format(time.RFC3339),
					FinishedOn:   in.Finished.UTC().Format(time.RFC3339),
				},
			},
		},
	}
}

func builderID(version, commit string) string {
	return fmt.Sprintf("%s@%s+%s", builderPrefix, version, commit)
}

// Write marshals st as indented JSON to <dir>/provenance.json and returns the
// path written.
func Write(dir string, st Statement) (string, error) {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Stats summarizes a HashTree walk, for a one-line progress notice.
type Stats struct {
	Files int
	Bytes int64
}

// HashTree returns one Subject per regular file under root, named
// "<prefix>/<relpath>" (slash-separated) with its sha256 digest. Directories,
// symlinks, and special files are skipped — only regular file contents are
// attested. A missing root yields no subjects and no error.
func HashTree(root, prefix string) ([]Subject, Stats, error) {
	var (
		subjects []Subject
		stats    Stats
	)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return filepath.SkipAll
			}
			return err
		}
		if !d.Type().IsRegular() { // skips dirs, symlinks, devices, sockets, fifos
			return nil
		}
		sum, n, err := hashFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if prefix != "" {
			name = prefix + "/" + name
		}
		subjects = append(subjects, Subject{Name: name, Digest: map[string]string{"sha256": sum}})
		stats.Files++
		stats.Bytes += n
		return nil
	})
	if err != nil {
		return nil, Stats{}, err
	}
	return subjects, stats, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Redact returns a copy of env suitable for recording in provenance. It is the
// identity function today; when the secret-masking roadmap item lands, secret
// values will be replaced here so they never reach the attestation.
func Redact(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

// SHA256Hex returns the hex sha256 of b, for hashing the workflow file.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
