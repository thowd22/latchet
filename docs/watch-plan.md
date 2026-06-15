# Plan — Global config (`latchet-ci.yml`) + `latchet watch`

Design for the two coupled [roadmap](../ROADMAP.md) items that turn latchet
into a minimal, cron-driven CI server. `watch` depends on the global config
(that's where watched repos live), so the global config ships first.

## Part 1 — Global config

A machine-wide config file, distinct from the per-project workflow
`latchet.yml`. Optional: with no file, latchet behaves exactly as today.

### Location (first match wins)

1. `$LATCHET_CONFIG` — explicit path override (also makes testing easy).
2. `$XDG_CONFIG_HOME/latchet/latchet-ci.yml`
3. `~/.config/latchet/latchet-ci.yml`

(Windows `%APPDATA%` is noted in the roadmap; deferred — Linux/macOS first.)

### Schema (strict; unknown keys rejected, like `internal/config`)

```yaml
runtime: podman                 # preferred container runtime
workspace_root: /var/lib/latchet/ws
log_dir: /var/log/latchet
max_parallel: 4                 # default job concurrency
env:                            # default env merged into every run
  CI: "true"
  REGISTRY: ghcr.io/me
watch:                          # repos for `latchet watch` (Part 2)
  - url: git@github.com:me/app.git
    branches: [main, release]
    tags: ["v*"]
  - url: git@github.com:me/lib.git
    branches: [main]
```

### Precedence (highest wins)

```
CLI flags  >  environment variables  >  global config  >  built-in defaults
```

Implementation keeps the existing env-reading code untouched:

- `runtime` / `workspace_root` / `log_dir` are applied by **setting the
  corresponding `LATCHET_*` env var only when it is unset**, so a real env var
  (and there are no flags for these) always wins, and the packages that already
  read those vars (`runtime`, `workspace`, `logstore`) need no changes.
- `max_parallel` is applied only when `-max-parallel` was **not** passed
  (detected via `flag.Visit`).
- `env` becomes `engine.Options.DefaultEnv`, merged **below** the workflow's
  own `env` (so a workflow always overrides a machine default), and above the
  built-in `LATCHET_*` vars:
  `builtins -> globalDefaultEnv -> workflow.env -> job.env -> step.env`.

## Part 2 — `latchet watch`

```sh
latchet watch            # one pass over configured repos, then exit
```

One-shot (no internal timer — schedule with cron). For each configured repo it
checks the watched branches/tags, and when a ref has advanced (or a new/moved
tag appears) it fetches that commit and runs the repo's `latchet.yml`.

### State (detect each change exactly once)

A JSON map persisted at `$XDG_STATE_HOME/latchet/watch/state.json`
(`~/.local/state/...` fallback), keyed by `url\x00<refname>` → last-seen SHA,
plus a per-(repo, tag-pattern) baseline marker.

### Decision logic (pure, unit-testable)

`decide(entry, remoteRefs, state) -> (fires, nextState)` where `remoteRefs`
comes from `git ls-remote` (branches under `refs/heads/`, tags under
`refs/tags/`):

- **Branches** (explicit names): key `url\x00refs/heads/<b>`.
  - key absent → record SHA, **no fire** (first-run baseline).
  - key present & SHA changed → **fire**, update.
- **Tag patterns** (globs, e.g. `v*`): per-(repo, pattern) baseline marker.
  - marker absent → record every currently-matching tag's SHA, set marker,
    **no fire** (first-run / new-pattern baseline).
  - marker present → for each matching tag: key absent (**new tag**) → fire +
    record; key present & SHA changed (**tag moved**) → fire + record.

This satisfies the roadmap's rules: first run is a no-op baseline; after that,
branch advances, new tags, and tag moves each fire exactly once.

### Firing a run

For each fire: shallow-clone the repo at the ref into a temp dir under the
workspace root, `chdir` in (so `builtinenv.ResolveGit` reports the fired ref's
branch/tag/SHA), run `engine.Run` on its `latchet.yml`, then restore the cwd
and remove the clone. Watch processes refs sequentially, so the `chdir` is
safe. State for a ref is updated **after** the run attempt regardless of
outcome — the change was detected once; a failing workflow should not re-fire
the same SHA every pass. A repo with no `latchet.yml` is logged and skipped
(still baselined). `git` errors for one repo are logged and don't abort the
pass.

### Transport & trust

latchet shells out to `git`; auth is whatever the user's environment provides.
The roadmap targets **SSH** URLs (the user's existing key; no HTTPS/token
handling) — latchet doesn't restrict the scheme, but SSH is the supported
transport. **Security:** `watch` runs whatever `latchet.yml` the remote repo
ships, inside the usual job containers — only watch repositories you trust.

### Out of scope (this slice)

- PR / merge-request triggers (branches and tags only).
- An internal timer (use cron).
- Per-run provenance recording the watch trigger as `externalParameters`
  (a noted follow-on once watch lands).
