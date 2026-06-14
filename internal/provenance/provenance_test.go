package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func sampleInput() Input {
	return Input{
		RunID:          "20260614T000000-abc123",
		Started:        time.Date(2026, 6, 14, 1, 2, 3, 0, time.UTC),
		Finished:       time.Date(2026, 6, 14, 1, 2, 9, 0, time.UTC),
		BuilderVersion: "v0.4.0",
		BuilderCommit:  "deadbee",
		WorkflowPath:   "latchet.yml",
		WorkflowSHA:    "ff00",
		Invocation:     map[string]string{"file": "latchet.yml", "max_parallel": "4"},
		Source:         &SourceRef{URI: "https://example/r", Ref: "refs/heads/main", Revision: "abcd"},
		Jobs: []JobParams{
			{ID: "build", Image: "golang:1.22", Steps: []StepParams{{Name: "go build", Run: "go build ./..."}}},
			{ID: "a", Image: "alpine:3.19", Steps: []StepParams{{Run: "echo hi"}}},
		},
		Images:   map[string]string{"golang:1.22": "docker.io/library/golang@sha256:bbb", "alpine:3.19": "docker.io/library/alpine@sha256:aaa"},
		Subjects: []Subject{{Name: "build/z.bin", Digest: map[string]string{"sha256": "2"}}, {Name: "build/a.bin", Digest: map[string]string{"sha256": "1"}}},
	}
}

func TestBuildDeterministicAndSorted(t *testing.T) {
	a, _ := json.Marshal(Build(sampleInput()))
	b, _ := json.Marshal(Build(sampleInput()))
	if string(a) != string(b) {
		t.Fatal("Build is not deterministic for identical input")
	}

	st := Build(sampleInput())
	if st.Type != StatementType || st.PredicateType != PredicateType {
		t.Errorf("wrong type URIs: %s / %s", st.Type, st.PredicateType)
	}
	// subjects sorted by name
	if st.Subject[0].Name != "build/a.bin" || st.Subject[1].Name != "build/z.bin" {
		t.Errorf("subjects not sorted: %+v", st.Subject)
	}
	// resolved deps sorted by image name; digest pinned
	deps := st.Predicate.BuildDefinition.ResolvedDependencies
	if deps[0].Name != "alpine:3.19" || deps[0].URI != "docker.io/library/alpine@sha256:aaa" {
		t.Errorf("deps not sorted/pinned: %+v", deps)
	}
	// jobs sorted by ID (a before build)
	jobs := st.Predicate.BuildDefinition.InternalParameters.Jobs
	if jobs[0].ID != "a" || jobs[1].ID != "build" {
		t.Errorf("jobs not sorted: %+v", jobs)
	}
	md := st.Predicate.RunDetails.Metadata
	if md.InvocationID != "20260614T000000-abc123" || md.StartedOn != "2026-06-14T01:02:03Z" {
		t.Errorf("bad metadata: %+v", md)
	}
	if got := st.Predicate.RunDetails.Builder.ID; got != "https://latchet.dev/builders/latchet@v0.4.0+deadbee" {
		t.Errorf("builder id = %q", got)
	}
}

func TestBuildEmptySubjectsFallsBackToWorkflow(t *testing.T) {
	in := sampleInput()
	in.Subjects = nil
	st := Build(in)
	if len(st.Subject) != 1 || st.Subject[0].Name != "latchet.yml" || st.Subject[0].Digest["sha256"] != "ff00" {
		t.Errorf("expected workflow-file fallback subject, got %+v", st.Subject)
	}
}

func TestHashTreeSkipsNonRegularAndPrefixes(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "sub", "b.txt"), "world")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
	}

	subs, stats, err := HashTree(dir, "job1")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 {
		t.Errorf("expected 2 regular files, got %d (symlink should be skipped)", stats.Files)
	}
	names := map[string]string{}
	for _, s := range subs {
		names[s.Name] = s.Digest["sha256"]
	}
	// sha256("hello")
	if names["job1/a.txt"] != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("bad hash/name for a.txt: %v", names)
	}
	if _, ok := names["job1/sub/b.txt"]; !ok {
		t.Errorf("nested file missing or mis-prefixed: %v", names)
	}
}

func TestHashTreeMissingRootIsEmpty(t *testing.T) {
	subs, stats, err := HashTree(filepath.Join(t.TempDir(), "nope"), "")
	if err != nil {
		t.Fatalf("missing root should not error: %v", err)
	}
	if len(subs) != 0 || stats.Files != 0 {
		t.Errorf("missing root should yield nothing, got %d", stats.Files)
	}
}

func TestWriteRoundTrips(t *testing.T) {
	dir := t.TempDir()
	p, err := Write(dir, Build(sampleInput()))
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, FileName) {
		t.Errorf("unexpected path %q", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var st Statement
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("written provenance is not valid JSON: %v", err)
	}
	if st.PredicateType != PredicateType {
		t.Errorf("round-trip lost predicateType")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
