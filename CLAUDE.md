# CLAUDE.md

Guidance for working in this repo. For the full rationale see
[`docs/design.md`](docs/design.md); for user-facing behavior see
[`README.md`](README.md); for deferred work see [`ROADMAP.md`](ROADMAP.md).

## What this is

`latchet` — a minimal, single-binary Docker/Podman-based CI/CD workflow
engine. It reads one `latchet.yml` (a small subset of GitHub Actions syntax),
runs each job in a container in `needs` dependency order (parallel by default),
and writes a persistent per-job log file. ~2.7k LOC Go; one third-party
dependency (`gopkg.in/yaml.v3`).

Module: `github.com/thowd22/latchet` · Go 1.22.

## Commands

```sh
go build -o latchet ./cmd/latchet          # build the binary
go test ./...                              # unit tests, no container runtime needed
go test -tags integration ./internal/...   # integration tests, need docker/podman
go vet ./...                               # vet (runs in CI)

./latchet                                  # run ./latchet.yml, jobs parallel up to NumCPU
./latchet -file ci/x.yml                   # alternate workflow file
./latchet -max-parallel 1                  # sequential; step output streamed live
./latchet -dry-run                         # print execution plan (waves) and exit
./latchet -validate-only                   # parse + validate and exit
```

Exit codes: `0` all jobs ok · `1` a job failed · `2` config/parse error ·
`3` infra (container/runtime/workspace) error. Defined in `internal/engine`.

## Layout & dependency direction

```
cmd/latchet/main.go    flag parsing, dispatch to engine.{Run,Validate,DryRun}
internal/
  config/      YAML schema, strict parsing (KnownFields), aggregated Validate()
  dag/         generic graph: topo Order, Waves, Kahn cycle detection
  envutil/     ordered env merge: built-in -> workflow -> job -> step
  keys/        uses: keys — parse url//subpath@ref, resolve tag->SHA, fetch+cache
  builtinenv/  LATCHET_* vars injected into every step (git facts, run/job ids)
  workspace/   per-run/per-job host dirs, inherit Seed(), cleanup policy
  runtime/     docker/podman detect + container lifecycle (pure argv builders)
  log/         plain-text job/step markers + run summary
  logstore/    persistent per-run log dir + `latest` symlink
  scheduler/   generic parallel runner: concurrency cap + skip propagation
  engine/      orchestrator wiring it all together
  version/     Version/Commit, stamped via -ldflags by the release pipeline
testdata/      sample workflows for integration tests
```

Imports are acyclic: `main` → `engine` → everything; `scheduler` → `dag`;
`engine` → `keys` → `config` (`config` never imports `keys`: resolved keys
are handed to it via the non-YAML `Workflow.Keys` field); `version` is leaf.
`dag` and `scheduler` are deliberately generic (know nothing about
jobs/containers) so they can't form import cycles and are tested
hermetically.

## Conventions that matter here

- **Strict YAML.** Unknown keys (`runs-on`, `timeout-minutes`, …) are rejected
  at parse time, not ignored. Adding a workflow field means touching
  `internal/config` schema + `Validate()`.
- **One container per job, `exec` per step** (not `docker run` per step). Steps
  run via `sh -c` with `set -e` prepended — no bash-isms.
- **Determinism is load-bearing.** `dag` emits the alphabetically-smallest
  ready node first; `envutil.Merge` returns sorted `KEY=VALUE`. Don't introduce
  map-iteration-order dependence.
- **Shell out to the CLI, not the Docker SDK** — keeps deps at one module and
  serves docker+podman from one path. `runtime` command builders are pure
  functions returning argv, table-tested.
- **`internal/` only** — no public Go API.
- Integration tests are behind `//go:build integration` and are NOT in CI
  (they need a real runtime). Run locally.

## Workflow-author-facing env vars (read by the binary)

`LATCHET_RUNTIME`, `LATCHET_WORKSPACE_ROOT`, `LATCHET_KEEP_WORKSPACE`,
`LATCHET_LOG_DIR`, `LATCHET_COSIGN_KEY`, `LATCHET_COSIGN_TLOG`,
`LATCHET_DETERMINISTIC`, `LATCHET_CONFIG` (global `latchet-ci.yml` path),
`LATCHET_WATCH_STATE` (`latchet watch` state file), `LATCHET_KEYS_CACHE`
(fetched-keys cache dir). Distinct from
the output-only `LATCHET_*` vars `builtinenv` *injects* into steps
(`LATCHET_WORKSPACE`, `LATCHET_RUN_ID`, `LATCHET_JOB_ID`, `LATCHET_GIT_*`).

Subcommands: `latchet verify <provenance.json>` (`engine.Verify`) and
`latchet watch` (`internal/watch`; cron-scheduled, runs watched repos'
`latchet.yml` on new commits/tags). Secrets: `secrets:` (workflow/job) names
host env vars injected into steps and masked in logs + provenance
(`internal/mask`, `provenance.Redact`). Global config: `internal/globalconfig`
(`latchet-ci.yml`) — also sets `location:` → `LATCHET_LOCATION` built-in.
Conditionals: `if:`/`elif:`/`else:` on steps and a single `if:` on jobs
(false job -> skipped, propagates to dependents), evaluated by `internal/cond`
(`$VAR`, `==`/`!=`/`&&`/`||`/`!`, parens) against the merged env.
`strategy.matrix` (`config.ExpandMatrix`): a job is fanned into one per combo
before the DAG; matrix vars set as env + `$`-expanded into `container:`.
Functions (`config.Function`, `config.ExpandCalls`): `functions:` in workflow
(local) or `latchet-ci.yml` (global, local shadows global via `MergeFunctions`);
a `call:` step with `with:` inputs inlines the function's steps into the job.
Keys (`internal/keys`): a `uses: <git url>[//<subpath>]@<ref>` step invokes a
*fetched* function — a `key.yml` in a remote repo (catalog:
`thowd22/latchet-keys`). Pinned to tag/SHA only; `keys.ResolveAll` runs in
`engine.loadAndValidate` before `Validate` (checking `with:` needs the key's
inputs), caches clones by SHA under `$XDG_CACHE_HOME/latchet/keys/`
(`LATCHET_KEYS_CACHE` overrides), and returns resolved
`git+url[//subpath]@sha` URIs recorded as provenance `resolvedDependencies`
(`latchet verify` re-pins to the recorded SHA). Fetch failures exit 3
(`keys.FetchError` via `exitFor`); bad refs/`key.yml` exit 2.
Step outputs: a step appends `NAME=value` to `$LATCHET_ENV`
(`builtinenv.EnvFileVar`, host-read from `jobDir/.latchet/env`); `engine.runJob`
merges them into later steps' env. A job's declared `outputs:` are stored
run-wide (`jobOutputs`) and injected as env into `needs:`-dependents (cross-job).

Each run emits `<logdir>/provenance.json` (SLSA v1.0, `internal/provenance`),
optionally signed via cosign (`internal/signer`) when `LATCHET_COSIGN_KEY` is
set. Both are best-effort and never change the exit code. `latchet verify
<provenance.json>` (`engine.Verify`) re-derives a run and compares subjects
(strict/lax), writing `verification.json`.
