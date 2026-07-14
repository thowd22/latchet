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
	"strings"
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
	If   string            `json:"if,omitempty"`
	Elif string            `json:"elif,omitempty"`
	Else bool              `json:"else,omitempty"`
}

// Dependency is a build input pinned to a resolved digest: container images
// resolved at pull time (URI is the @sha256-pinned image ref) and fetched
// keys resolved at fetch time (URI is git+<url>[//<subpath>]@<sha>).
type Dependency struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// keyURIPrefix marks a resolvedDependencies entry as a fetched key.
const keyURIPrefix = "git+"

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
	Keys     map[string]string // uses: ref as written -> resolved git+url[//subpath]@sha URI
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

	deps := make([]Dependency, 0, len(in.Images)+len(in.Keys))
	for ref, digest := range in.Images {
		deps = append(deps, Dependency{URI: digest, Name: ref})
	}
	for ref, uri := range in.Keys {
		deps = append(deps, Dependency{URI: uri, Name: ref})
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
		// .latchet is latchet's per-job metadata dir (step-output env file); it
		// is not a build artifact, so it never appears as a provenance subject.
		if d.IsDir() && d.Name() == ".latchet" && filepath.Dir(p) == root {
			return filepath.SkipDir
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

// Redact returns a copy of env with every value that contains a secret value
// replaced entirely by "***", so declared secrets never reach the attestation.
// Empty secrets are ignored.
func Redact(env map[string]string, secrets []string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = RedactString(v, secrets)
	}
	return out
}

// RedactString replaces the whole string with "***" if it contains any secret
// value, and otherwise returns it unchanged. Used for env values and per-step
// run strings recorded in provenance.
func RedactString(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" && strings.Contains(s, sec) {
			return "***"
		}
	}
	return s
}

// SHA256Hex returns the hex sha256 of b, for hashing the workflow file.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Load reads and parses a provenance.json file. It checks the type URIs so a
// non-provenance JSON file is rejected with a clear error rather than yielding
// an empty statement.
func Load(path string) (Statement, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Statement{}, err
	}
	var st Statement
	if err := json.Unmarshal(b, &st); err != nil {
		return Statement{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if st.PredicateType != PredicateType {
		return Statement{}, fmt.Errorf("%s: not a SLSA provenance statement (predicateType=%q)", path, st.PredicateType)
	}
	return st, nil
}

// SubjectDigests maps each subject name to its sha256 digest.
func (s Statement) SubjectDigests() map[string]string {
	out := make(map[string]string, len(s.Subject))
	for _, sub := range s.Subject {
		out[sub.Name] = sub.Digest["sha256"]
	}
	return out
}

// ResolvedImages maps each image reference (as written in the workflow) to the
// digest-pinned URI recorded in resolvedDependencies. Key entries (git+ URIs)
// are excluded.
func (s Statement) ResolvedImages() map[string]string {
	deps := s.Predicate.BuildDefinition.ResolvedDependencies
	out := make(map[string]string, len(deps))
	for _, d := range deps {
		if d.Name != "" && !strings.HasPrefix(d.URI, keyURIPrefix) {
			out[d.Name] = d.URI
		}
	}
	return out
}

// ResolvedKeys maps each uses: reference (as written in the workflow) to the
// SHA-pinned git+<url>[//<subpath>]@<sha> URI recorded in
// resolvedDependencies.
func (s Statement) ResolvedKeys() map[string]string {
	deps := s.Predicate.BuildDefinition.ResolvedDependencies
	out := map[string]string{}
	for _, d := range deps {
		if d.Name != "" && strings.HasPrefix(d.URI, keyURIPrefix) {
			out[d.Name] = d.URI
		}
	}
	return out
}

// WorkflowDigest returns the recorded sha256 of the workflow file.
func (s Statement) WorkflowDigest() string {
	return s.Predicate.BuildDefinition.ExternalParameters.Workflow.Digest["sha256"]
}

// WorkflowPath returns the workflow file path recorded at run time.
func (s Statement) WorkflowPath() string {
	return s.Predicate.BuildDefinition.ExternalParameters.Workflow.Path
}
