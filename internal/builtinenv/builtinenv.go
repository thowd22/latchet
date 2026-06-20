// Package builtinenv computes the env vars latchet injects into every step
// automatically. They form the lowest-precedence layer of the env merge:
// user-defined env (workflow -> job -> step) overrides them, so a workflow
// can fake any of them for testing.
//
// Every name is LATCHET_*-prefixed so it cannot collide with workflow- or
// image-defined variables. These are output-only (injected into steps) and
// are distinct from the LATCHET_* vars the binary *reads* to configure itself
// (LATCHET_RUNTIME, LATCHET_WORKSPACE_ROOT, LATCHET_KEEP_WORKSPACE,
// LATCHET_LOG_DIR), which are reserved on input.
package builtinenv

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// Injected variable names.
const (
	// Workspace is the container-side workspace path (always /workspace in
	// v2/v3); named so scripts can stay path-agnostic.
	Workspace = "LATCHET_WORKSPACE"
	// RunID is latchet's run id (matches the workspace and log dir name).
	RunID = "LATCHET_RUN_ID"
	// JobID is the current job's id.
	JobID = "LATCHET_JOB_ID"

	GitURL    = "LATCHET_GIT_URL"
	GitBranch = "LATCHET_GIT_BRANCH"
	GitTag    = "LATCHET_GIT_TAG"
	GitSHA    = "LATCHET_GIT_SHA"
	GitRef    = "LATCHET_GIT_REF"

	// LocationVar identifies where the run is executing (e.g. "server" vs
	// "local"); resolved from the LATCHET_LOCATION env var (set by the global
	// config), defaulting to "local".
	LocationVar = "LATCHET_LOCATION"
)

// DefaultLocation is used when LATCHET_LOCATION is unset.
const DefaultLocation = "local"

// Location returns the run location: the LATCHET_LOCATION env var if set
// (the global config fills it via ApplyEnvDefaults), else "local".
func Location() string {
	if v := os.Getenv(LocationVar); v != "" {
		return v
	}
	return DefaultLocation
}

// Git holds the source-control facts injected as LATCHET_GIT_* vars. Any field
// that cannot be determined is the empty string.
type Git struct {
	URL    string // remote URL (origin)
	Branch string // branch name when on a branch
	Tag    string // tag name when HEAD is exactly a tag
	SHA    string // full commit SHA of HEAD
	Ref    string // full ref, e.g. refs/heads/main or refs/tags/v1.0.0
	// CommitEpoch is HEAD's commit time as a Unix-seconds string, used as
	// SOURCE_DATE_EPOCH by the determinism helpers. Empty when unavailable;
	// the engine fills a run-start fallback in that case.
	CommitEpoch string
}

// ResolveGit gathers git facts from the host working directory by shelling out
// to git. It is best-effort: a missing git binary, a non-git directory, or any
// failing sub-command leaves the affected fields empty rather than erroring.
//
// This is the fallback source used when no `latchet watch` trigger supplied the
// values; once watch lands, a trigger's known ref/SHA take precedence.
func ResolveGit(ctx context.Context) Git {
	g := Git{
		URL:         runGit(ctx, "remote", "get-url", "origin"),
		Branch:      runGit(ctx, "symbolic-ref", "--short", "HEAD"),
		Tag:         runGit(ctx, "describe", "--tags", "--exact-match"),
		SHA:         runGit(ctx, "rev-parse", "HEAD"),
		CommitEpoch: runGit(ctx, "show", "-s", "--format=%ct", "HEAD"),
	}
	g.Ref = DeriveRef(g.Branch, g.Tag)
	return g
}

// Deterministic returns the environment the determinism helpers inject into a
// job's steps to remove the cheapest sources of build nondeterminism. These
// sit at built-in (lowest) precedence so a workflow can override any of them.
// SOURCE_DATE_EPOCH is HEAD's commit time when known (stable across re-runs of
// the same commit), else the engine-provided run-start fallback in g.CommitEpoch.
func Deterministic(g Git) map[string]string {
	return map[string]string{
		"SOURCE_DATE_EPOCH": g.CommitEpoch,
		"LC_ALL":            "C",
		"LANG":              "C",
		"TZ":                "UTC",
	}
}

// OverrideRef returns g with its Branch/Tag/Ref set from a known full refname
// (refs/heads/<b> or refs/tags/<t>). Used when the caller knows the ref a
// detached checkout can't report — e.g. `latchet watch`, which checks out a
// commit by SHA. SHA/URL/CommitEpoch are left as probed. An unrecognized ref
// shape leaves g unchanged.
func OverrideRef(g Git, ref string) Git {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		g.Branch = strings.TrimPrefix(ref, "refs/heads/")
		g.Tag = ""
	case strings.HasPrefix(ref, "refs/tags/"):
		g.Tag = strings.TrimPrefix(ref, "refs/tags/")
		g.Branch = ""
	default:
		return g
	}
	g.Ref = DeriveRef(g.Branch, g.Tag)
	return g
}

// DeriveRef builds the full ref string from a branch or tag name, preferring a
// branch when both are present. Returns "" when neither is known (e.g. a
// detached HEAD at an untagged commit).
func DeriveRef(branch, tag string) string {
	switch {
	case branch != "":
		return "refs/heads/" + branch
	case tag != "":
		return "refs/tags/" + tag
	default:
		return ""
	}
}

// For builds the built-in env map for one job. workspace is the container-side
// workspace path (typically "/workspace").
func For(runID, jobID, workspace string, git Git) map[string]string {
	return map[string]string{
		Workspace:   workspace,
		RunID:       runID,
		JobID:       jobID,
		GitURL:      git.URL,
		GitBranch:   git.Branch,
		GitTag:      git.Tag,
		GitSHA:      git.SHA,
		GitRef:      git.Ref,
		LocationVar: Location(),
	}
}

// runGit runs git with args in the current working directory and returns its
// trimmed stdout, or "" if git is absent or the command fails. stderr is
// discarded so best-effort probes stay quiet.
func runGit(ctx context.Context, args ...string) string {
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
