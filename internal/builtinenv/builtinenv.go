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
)

// Git holds the source-control facts injected as LATCHET_GIT_* vars. Any field
// that cannot be determined is the empty string.
type Git struct {
	URL    string // remote URL (origin)
	Branch string // branch name when on a branch
	Tag    string // tag name when HEAD is exactly a tag
	SHA    string // full commit SHA of HEAD
	Ref    string // full ref, e.g. refs/heads/main or refs/tags/v1.0.0
}

// ResolveGit gathers git facts from the host working directory by shelling out
// to git. It is best-effort: a missing git binary, a non-git directory, or any
// failing sub-command leaves the affected fields empty rather than erroring.
//
// This is the fallback source used when no `latchet watch` trigger supplied the
// values; once watch lands, a trigger's known ref/SHA take precedence.
func ResolveGit(ctx context.Context) Git {
	g := Git{
		URL:    runGit(ctx, "remote", "get-url", "origin"),
		Branch: runGit(ctx, "symbolic-ref", "--short", "HEAD"),
		Tag:    runGit(ctx, "describe", "--tags", "--exact-match"),
		SHA:    runGit(ctx, "rev-parse", "HEAD"),
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
		Workspace: workspace,
		RunID:     runID,
		JobID:     jobID,
		GitURL:    git.URL,
		GitBranch: git.Branch,
		GitTag:    git.Tag,
		GitSHA:    git.SHA,
		GitRef:    git.Ref,
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
