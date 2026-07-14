package keys

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thowd22/latchet/internal/config"
	"gopkg.in/yaml.v3"
)

// FetchError marks a git/network/cache-IO failure while resolving or fetching
// a key — an environmental problem (engine exit 3), as opposed to a spec
// error in the reference or key.yml (exit 2).
type FetchError struct{ Err error }

func (e *FetchError) Error() string { return e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

// CacheDir returns the SHA-addressed key cache directory,
// $XDG_CACHE_HOME/latchet/keys (via os.UserCacheDir), creating it if needed.
// LATCHET_KEYS_CACHE overrides the location.
func CacheDir() (string, error) {
	dir := os.Getenv("LATCHET_KEYS_CACHE")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "latchet", "keys")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// resolve pins a parsed reference to a commit SHA. A SHA ref passes through
// without touching the network; anything else must resolve as a tag via one
// `git ls-remote` call. A ref that only exists as a branch is rejected: keys
// must be pinned to something immutable.
func resolve(ctx context.Context, ref Ref) (string, error) {
	if IsSHA(ref.RefName) {
		return ref.RefName, nil
	}
	// The tag pattern is a glob so the peeled "^{}" line of an annotated tag
	// is included (an exact pattern would omit it); over-matches like v10 for
	// v1 are filtered by the exact-name switch below.
	out, err := exec.CommandContext(ctx, "git", "ls-remote", ref.URL,
		"refs/tags/"+ref.RefName+"*", "refs/heads/"+ref.RefName).Output()
	if err != nil {
		return "", &FetchError{fmt.Errorf("key %q: git ls-remote %s: %w", ref.Raw, ref.URL, err)}
	}
	var tagSHA, peeledSHA, branchSHA string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sha, name := fields[0], fields[1]
		switch name {
		case "refs/tags/" + ref.RefName:
			tagSHA = sha
		case "refs/tags/" + ref.RefName + "^{}": // annotated tag: the commit it points to
			peeledSHA = sha
		case "refs/heads/" + ref.RefName:
			branchSHA = sha
		}
	}
	switch {
	case peeledSHA != "":
		return peeledSHA, nil
	case tagSHA != "":
		return tagSHA, nil
	case branchSHA != "":
		return "", fmt.Errorf("key %q: %q resolves to a branch; keys must be pinned to a tag or commit SHA", ref.Raw, ref.RefName)
	default:
		return "", fmt.Errorf("key %q: no tag %q in %s", ref.Raw, ref.RefName, ref.URL)
	}
}

// fetch materializes the repo at sha under <cacheDir>/<sha> and returns that
// path. The cache is content-addressed and immutable: a hit returns without
// touching the network (SHA-pinned keys work fully offline once cached). A
// miss clones into a temp dir inside cacheDir, checks out the SHA, strips
// .git, and renames into place; losing a rename race to a concurrent run is
// success.
func fetch(ctx context.Context, ref Ref, sha, cacheDir string) (string, error) {
	dst := filepath.Join(cacheDir, sha)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	tmp, err := os.MkdirTemp(cacheDir, "fetch-")
	if err != nil {
		return "", &FetchError{fmt.Errorf("key %q: %w", ref.Raw, err)}
	}
	defer os.RemoveAll(tmp) // no-op after a successful rename
	if o, err := exec.CommandContext(ctx, "git", "clone", "--quiet", ref.URL, tmp).CombinedOutput(); err != nil {
		return "", &FetchError{fmt.Errorf("key %q: git clone %s: %w\n%s", ref.Raw, ref.URL, err, o)}
	}
	if o, err := exec.CommandContext(ctx, "git", "-C", tmp, "checkout", "--quiet", "--detach", sha).CombinedOutput(); err != nil {
		// The SHA isn't in the clone: a spec error (bad pin), not an infra one.
		return "", fmt.Errorf("key %q: commit %s not found in %s: %w\n%s", ref.Raw, sha[:12], ref.URL, err, o)
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return "", &FetchError{fmt.Errorf("key %q: %w", ref.Raw, err)}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if _, statErr := os.Stat(dst); statErr == nil {
			return dst, nil // lost the race to a concurrent run; cache is valid
		}
		return "", &FetchError{fmt.Errorf("key %q: %w", ref.Raw, err)}
	}
	return dst, nil
}

// load strict-parses <dir>/<subpath>/key.yml into a Function and checks the
// key-body rules: at least one step, every step a plain run: (no call:,
// uses:, or if/elif/else — the same no-nesting rule as workflow functions),
// and valid input names.
func load(ref Ref, dir string) (*config.Function, error) {
	path := filepath.Join(dir, filepath.FromSlash(ref.Subpath), "key.yml")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			where := "repository root"
			if ref.Subpath != "" {
				where = fmt.Sprintf("%q", ref.Subpath)
			}
			return nil, fmt.Errorf("key %q: no key.yml at %s", ref.Raw, where)
		}
		return nil, &FetchError{fmt.Errorf("key %q: %w", ref.Raw, err)}
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var fn config.Function
	if err := dec.Decode(&fn); err != nil {
		return nil, fmt.Errorf("key %q: parsing key.yml: %w", ref.Raw, err)
	}

	var errs []string
	if len(fn.Steps) == 0 {
		errs = append(errs, "has no steps")
	}
	inputs := make([]string, 0, len(fn.Inputs))
	for in := range fn.Inputs {
		inputs = append(inputs, in)
	}
	sort.Strings(inputs)
	for _, in := range inputs {
		if !validEnvName(in) {
			errs = append(errs, fmt.Sprintf("input %q is not a valid env var name", in))
		}
	}
	for i, st := range fn.Steps {
		n := i + 1
		switch {
		case st == nil:
			errs = append(errs, fmt.Sprintf("step %d is empty", n))
		case st.Call != "" || st.Uses != "":
			errs = append(errs, fmt.Sprintf("step %d: a key step must be a plain run: (no call:/uses:)", n))
		case st.If != "" || st.Elif != "" || st.Else:
			errs = append(errs, fmt.Sprintf("step %d: a key step cannot have if/elif/else", n))
		case strings.TrimSpace(st.Run) == "":
			errs = append(errs, fmt.Sprintf("step %d has an empty 'run'", n))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("key %q: invalid key.yml:\n  - %s", ref.Raw, strings.Join(errs, "\n  - "))
	}
	return &fn, nil
}

// validEnvName mirrors config's env var name rule ([A-Za-z_][A-Za-z0-9_]*).
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

// ResolveAll fetches every distinct `uses:` reference in the workflow and
// returns the resolved keys (verbatim uses string -> function) plus the
// provenance map (uses string -> git+url[//subpath]@sha URI). Jobs and steps
// are scanned in deterministic order; each unique reference is resolved once.
// Non-cached fetches log a single line to out.
func ResolveAll(ctx context.Context, wf *config.Workflow, out io.Writer) (map[string]*config.Function, map[string]string, error) {
	var refs []string
	seen := map[string]bool{}
	jobIDs := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		jobIDs = append(jobIDs, id)
	}
	sort.Strings(jobIDs)
	for _, id := range jobIDs {
		for _, st := range wf.Jobs[id].Steps {
			if st == nil || st.Uses == "" || seen[st.Uses] {
				continue
			}
			seen[st.Uses] = true
			refs = append(refs, st.Uses)
		}
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}

	cacheDir, err := CacheDir()
	if err != nil {
		return nil, nil, &FetchError{fmt.Errorf("keys cache: %w", err)}
	}

	fns := make(map[string]*config.Function, len(refs))
	resolved := make(map[string]string, len(refs))
	for _, raw := range refs {
		ref, err := ParseRef(raw)
		if err != nil {
			return nil, nil, err
		}
		sha, err := resolve(ctx, ref)
		if err != nil {
			return nil, nil, err
		}
		cached := true
		if _, err := os.Stat(filepath.Join(cacheDir, sha)); err != nil {
			cached = false
		}
		dir, err := fetch(ctx, ref, sha, cacheDir)
		if err != nil {
			return nil, nil, err
		}
		if !cached && out != nil {
			fmt.Fprintf(out, "latchet: fetched key %s @ %s\n", raw, sha[:12])
		}
		fn, err := load(ref, dir)
		if err != nil {
			return nil, nil, err
		}
		fns[raw] = fn
		resolved[raw] = ref.resolvedURI(sha)
	}
	return fns, resolved, nil
}
