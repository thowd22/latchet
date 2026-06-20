package config

import (
	"reflect"
	"sort"
	"testing"
)

func TestExpandMatrixCartesianAndNeeds(t *testing.T) {
	wf := &Workflow{Jobs: map[string]*Job{
		"test": {
			ID:        "test",
			Container: "golang:${go}",
			Strategy:  &Strategy{Matrix: map[string][]string{"go": {"1.21", "1.22"}, "os": {"linux"}}},
			Steps:     []*Step{{Run: "go test ./..."}},
		},
		"report": {
			ID:        "report",
			Container: "alpine",
			Needs:     StringOrSlice{"test"},
			Steps:     []*Step{{Run: "echo done"}},
		},
	}}

	out := ExpandMatrix(wf)

	// test expands into 2 jobs (go x os = 2x1); report + 2 = 3 total.
	if len(out.Jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d: %v", len(out.Jobs), jobIDs(out))
	}
	for _, want := range []string{"test (go=1.21, os=linux)", "test (go=1.22, os=linux)"} {
		j := out.Jobs[want]
		if j == nil {
			t.Fatalf("missing expansion %q (have %v)", want, jobIDs(out))
		}
		if j.Strategy != nil {
			t.Errorf("expansion %q kept its Strategy", want)
		}
	}
	// container is $-expanded with the matrix value.
	if got := out.Jobs["test (go=1.21, os=linux)"].Container; got != "golang:1.21" {
		t.Errorf("container = %q, want golang:1.21", got)
	}
	// matrix vars are injected as env.
	if env := out.Jobs["test (go=1.22, os=linux)"].Env; env["go"] != "1.22" || env["os"] != "linux" {
		t.Errorf("matrix env wrong: %v", env)
	}
	// report's needs now points at both expansions.
	got := []string(out.Jobs["report"].Needs)
	sort.Strings(got)
	want := []string{"test (go=1.21, os=linux)", "test (go=1.22, os=linux)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("report needs = %v, want %v", got, want)
	}
}

func TestExpandMatrixNoMatrixIsUnchanged(t *testing.T) {
	wf := &Workflow{Jobs: map[string]*Job{"a": {ID: "a", Container: "x", Steps: []*Step{{Run: "echo"}}}}}
	if out := ExpandMatrix(wf); out != wf {
		t.Errorf("workflow with no matrix should be returned unchanged")
	}
}

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"go": "1.22", "os": "linux"}
	cases := map[string]string{
		"golang:${go}":      "golang:1.22",
		"img:$go-$os":       "img:1.22-linux",
		"plain":             "plain",
		"keep:$unknown":     "keep:$unknown", // unknown var left literal
		"${go}/${os}/extra": "1.22/linux/extra",
	}
	for in, want := range cases {
		if got := expandVars(in, vars); got != want {
			t.Errorf("expandVars(%q) = %q, want %q", in, got, want)
		}
	}
}

func jobIDs(wf *Workflow) []string {
	ids := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
