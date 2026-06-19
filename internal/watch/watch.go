// Package watch implements `latchet watch`: a one-shot pass that checks the
// repositories configured in the global latchet-ci.yml for new commits on
// watched branches and tags, and runs a repo's latchet.yml when a watched ref
// advances (or a new/moved tag appears). It has no internal timer — schedule
// it with cron.
//
// The decision logic (decide) is a pure function over the configured entry,
// the current remote refs, and the persisted last-seen state, so the
// fire-exactly-once semantics are unit-tested without git or containers.
package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thowd22/latchet/internal/engine"
	"github.com/thowd22/latchet/internal/globalconfig"
)

const sep = "\x00"

// RemoteRefs is the branch/tag → SHA map for one repository, from git ls-remote.
type RemoteRefs struct {
	Branches map[string]string
	Tags     map[string]string
}

// Fire is a single workflow run to perform: the repo, the ref that changed, and
// the commit to check out.
type Fire struct {
	URL string
	Ref string // full refname, e.g. refs/heads/main or refs/tags/v1.0.0
	SHA string
}

// Options configures a watch pass.
type Options struct {
	MaxParallel int
	DefaultEnv  map[string]string
	Stdout      io.Writer
	Stderr      io.Writer
}

// decide computes which refs fire and the next persisted state, given a watched
// entry, the current remote refs, and the prior state. It never mutates state.
//
//   - Branch: first sight records a baseline (no fire); a changed SHA fires.
//   - Tag pattern: the first pass for a (repo, pattern) baselines every matching
//     tag (no fire) and sets a marker; afterward a new matching tag fires on
//     first sight and a moved tag fires on SHA change.
func decide(entry globalconfig.WatchEntry, refs RemoteRefs, state map[string]string) ([]Fire, map[string]string) {
	next := make(map[string]string, len(state))
	for k, v := range state {
		next[k] = v
	}
	var fires []Fire

	for _, b := range entry.Branches {
		sha, ok := refs.Branches[b]
		if !ok {
			continue // configured branch doesn't exist on the remote
		}
		key := entry.URL + sep + "refs/heads/" + b
		prev, seen := state[key]
		if !seen {
			next[key] = sha // baseline, no fire
		} else if prev != sha {
			fires = append(fires, Fire{entry.URL, "refs/heads/" + b, sha})
			next[key] = sha
		}
	}

	for _, pat := range entry.Tags {
		marker := entry.URL + sep + "pattern:" + pat
		_, baselined := state[marker]
		names := matchingTags(refs.Tags, pat)
		if !baselined {
			next[marker] = "1"
			for _, name := range names {
				next[entry.URL+sep+"refs/tags/"+name] = refs.Tags[name]
			}
			continue
		}
		for _, name := range names {
			sha := refs.Tags[name]
			key := entry.URL + sep + "refs/tags/" + name
			if prev, seen := state[key]; !seen || prev != sha {
				fires = append(fires, Fire{entry.URL, "refs/tags/" + name, sha})
				next[key] = sha
			}
		}
	}

	sort.Slice(fires, func(i, j int) bool { return fires[i].Ref < fires[j].Ref })
	return fires, next
}

// matchingTags returns the sorted names of tags matching the glob pattern.
func matchingTags(tags map[string]string, pattern string) []string {
	var out []string
	for name := range tags {
		if ok, _ := path.Match(pattern, name); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Run performs one watch pass and returns a process exit code (0 ok, 1 a fired
// run failed, 2 no config, 3 state/infra error).
func Run(cfg *globalconfig.Config, opts Options) int {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	out, warn := opts.Stdout, opts.Stderr

	if len(cfg.Watch) == 0 {
		fmt.Fprintln(warn, "latchet watch: no repositories configured (set watch: in latchet-ci.yml)")
		return engine.ExitConfig
	}

	sp, err := statePath()
	if err != nil {
		fmt.Fprintf(warn, "latchet watch: %v\n", err)
		return engine.ExitInfra
	}
	state, err := loadState(sp)
	if err != nil {
		fmt.Fprintf(warn, "latchet watch: reading state: %v\n", err)
		return engine.ExitInfra
	}

	ctx := context.Background()
	fired, failed := 0, 0
	for _, entry := range cfg.Watch {
		refs, err := lsRemote(ctx, entry.URL)
		if err != nil {
			fmt.Fprintf(warn, "latchet watch: %s: %v\n", entry.URL, err)
			continue
		}
		fires, nextState := decide(entry, refs, state)
		state = nextState // detected once: state advances even if a fire fails
		for _, f := range fires {
			fmt.Fprintf(out, "latchet watch: %s %s @ %s — running latchet.yml\n", f.URL, f.Ref, short(f.SHA))
			if err := fire(ctx, f, opts); err != nil {
				fmt.Fprintf(warn, "latchet watch: %v\n", err)
				failed++
			} else {
				fired++
			}
		}
	}

	if err := saveState(sp, state); err != nil {
		fmt.Fprintf(warn, "latchet watch: writing state: %v\n", err)
		return engine.ExitInfra
	}
	fmt.Fprintf(out, "latchet watch: %d run(s) fired, %d failed\n", fired, failed)
	if failed > 0 {
		return engine.ExitFailed
	}
	return engine.ExitSuccess
}

// fire clones the repo at the fired commit, runs its latchet.yml from that
// checkout (chdir so the LATCHET_GIT_* facts reflect the fired ref), then
// cleans up.
func fire(ctx context.Context, f Fire, opts Options) error {
	dir, err := os.MkdirTemp("", "latchet-watch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if o, err := exec.CommandContext(ctx, "git", "clone", "--quiet", f.URL, dir).CombinedOutput(); err != nil {
		return fmt.Errorf("clone %s: %w\n%s", f.URL, err, o)
	}
	if o, err := exec.CommandContext(ctx, "git", "-C", dir, "checkout", "--quiet", f.SHA).CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s %s: %w\n%s", f.URL, short(f.SHA), err, o)
	}
	if !fileExists(filepath.Join(dir, "latchet.yml")) {
		fmt.Fprintf(opts.Stderr, "latchet watch: %s %s has no latchet.yml; skipping\n", f.URL, f.Ref)
		return nil
	}

	prev, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	defer os.Chdir(prev)

	code := engine.Run(engine.Options{
		File:        "latchet.yml",
		MaxParallel: opts.MaxParallel,
		DefaultEnv:  opts.DefaultEnv,
		Stdout:      opts.Stdout,
		Stderr:      opts.Stderr,
	})
	if code != engine.ExitSuccess {
		return fmt.Errorf("run for %s %s exited %d", f.URL, f.Ref, code)
	}
	return nil
}

func lsRemote(ctx context.Context, url string) (RemoteRefs, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-remote", url).Output()
	if err != nil {
		return RemoteRefs{}, fmt.Errorf("git ls-remote: %w", err)
	}
	return parseLsRemote(string(out)), nil
}

// parseLsRemote turns `git ls-remote` output into RemoteRefs. For annotated
// tags, the peeled "^{}" line (the commit the tag points to) overrides the tag
// object SHA.
func parseLsRemote(s string) RemoteRefs {
	r := RemoteRefs{Branches: map[string]string{}, Tags: map[string]string{}}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sha, ref := fields[0], fields[1]
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			r.Branches[strings.TrimPrefix(ref, "refs/heads/")] = sha
		case strings.HasPrefix(ref, "refs/tags/"):
			r.Tags[strings.TrimSuffix(strings.TrimPrefix(ref, "refs/tags/"), "^{}")] = sha
		}
	}
	return r
}

func statePath() (string, error) {
	if v := os.Getenv("LATCHET_WATCH_STATE"); v != "" {
		if err := os.MkdirAll(filepath.Dir(v), 0o755); err != nil {
			return "", err
		}
		return v, nil
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "latchet", "watch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

func loadState(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

func saveState(path string, state map[string]string) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
