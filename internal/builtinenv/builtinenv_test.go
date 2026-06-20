package builtinenv

import (
	"reflect"
	"testing"
)

func TestDeriveRef(t *testing.T) {
	tests := []struct {
		name, branch, tag, want string
	}{
		{"branch", "main", "", "refs/heads/main"},
		{"tag", "", "v1.0.0", "refs/tags/v1.0.0"},
		{"branch wins over tag", "main", "v1.0.0", "refs/heads/main"},
		{"neither", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveRef(tt.branch, tt.tag); got != tt.want {
				t.Fatalf("DeriveRef(%q, %q) = %q, want %q", tt.branch, tt.tag, got, tt.want)
			}
		})
	}
}

func TestFor(t *testing.T) {
	git := Git{
		URL:    "git@github.com:thowd22/latchet.git",
		Branch: "main",
		Tag:    "",
		SHA:    "deadbeef",
		Ref:    "refs/heads/main",
	}
	t.Setenv("LATCHET_LOCATION", "") // default to "local"
	got := For("20260611T120000-abc123", "build", "/workspace", git)
	want := map[string]string{
		"LATCHET_WORKSPACE":  "/workspace",
		"LATCHET_RUN_ID":     "20260611T120000-abc123",
		"LATCHET_JOB_ID":     "build",
		"LATCHET_GIT_URL":    "git@github.com:thowd22/latchet.git",
		"LATCHET_GIT_BRANCH": "main",
		"LATCHET_GIT_TAG":    "",
		"LATCHET_GIT_SHA":    "deadbeef",
		"LATCHET_GIT_REF":    "refs/heads/main",
		"LATCHET_LOCATION":   "local",
		"LATCHET_ENV":        "/workspace/.latchet/env",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("For() = %v, want %v", got, want)
	}
}

// Built-in vars are the lowest-precedence layer: a workflow can override any of
// them. This guards the invariant the engine relies on when ordering the merge.
func TestForIsOverridable(t *testing.T) {
	got := For("run1", "job1", "/workspace", Git{})
	if _, ok := got[Workspace]; !ok {
		t.Fatalf("For() missing %s", Workspace)
	}
	// All values present even when git is entirely empty (empty strings, not
	// absent keys) so downstream consumers see a stable key set.
	for _, k := range []string{GitURL, GitBranch, GitTag, GitSHA, GitRef} {
		if _, ok := got[k]; !ok {
			t.Fatalf("For() missing %s for empty Git", k)
		}
	}
}

func TestDeterministic(t *testing.T) {
	got := Deterministic(Git{CommitEpoch: "1700000000"})
	want := map[string]string{
		"SOURCE_DATE_EPOCH": "1700000000",
		"LC_ALL":            "C",
		"LANG":              "C",
		"TZ":                "UTC",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Deterministic()[%s] = %q, want %q", k, got[k], v)
		}
	}
}

func TestOverrideRef(t *testing.T) {
	base := Git{Branch: "", Tag: "", SHA: "abc", URL: "u"}
	// Branch ref: sets branch, clears tag, derives ref; SHA/URL untouched.
	b := OverrideRef(base, "refs/heads/main")
	if b.Branch != "main" || b.Tag != "" || b.Ref != "refs/heads/main" || b.SHA != "abc" || b.URL != "u" {
		t.Errorf("branch override: %+v", b)
	}
	// Tag ref: sets tag, clears branch.
	tg := OverrideRef(Git{Branch: "stale"}, "refs/tags/v1.0.0")
	if tg.Tag != "v1.0.0" || tg.Branch != "" || tg.Ref != "refs/tags/v1.0.0" {
		t.Errorf("tag override: %+v", tg)
	}
	// Unknown ref shape: unchanged.
	u := OverrideRef(Git{Branch: "keep"}, "refs/pull/7/head")
	if u.Branch != "keep" {
		t.Errorf("unknown ref should not change git: %+v", u)
	}
}

func TestLocation(t *testing.T) {
	t.Setenv("LATCHET_LOCATION", "")
	if got := Location(); got != "local" {
		t.Errorf("default Location() = %q, want local", got)
	}
	t.Setenv("LATCHET_LOCATION", "server")
	if got := Location(); got != "server" {
		t.Errorf("Location() = %q, want server", got)
	}
}
