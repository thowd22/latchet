# latchet — Design

This document records how `latchet` is built and *why*. For what it does and
how to use it, see [`README.md`](../README.md); for what is deferred, see
[`ROADMAP.md`](../ROADMAP.md).

## Overview

`latchet` is a minimal, fast Docker/Podman-based CI/CD workflow engine: a
single Go binary that reads one `latchet.yml` file (a small subset of
GitHub Actions syntax), runs each job inside a container in dependency
order (in parallel by default), and writes a persistent log file per job.

**Scope (GitHub Actions subset):** top-level `name` + `jobs`; per-job
`container`, `env`, `needs`, `steps`; per-step `name`, `run`, `env`; `env`
merging across the three levels. Everything else is rejected at parse time.

## Project layout

```
cmd/latchet/main.go        entry: parses flags, resolves the workflow path, dispatches
internal/
  config/                  YAML schema, strict parsing, aggregated validation
  dag/                     topological order + parallel-wave grouping + cycle detection
  envutil/                 ordered env merge (workflow -> job -> step)
  workspace/               per-run/per-job host directories, cleanup policy
  runtime/                 docker/podman detection + container lifecycle
  log/                     plain-text job/step markers + run summary
  logstore/                persistent per-run log directory + `latest` symlink
  scheduler/               parallel job runner with concurrency cap and skip propagation
  engine/                  orchestrator tying it all together
  version/                 build-time Version/Commit stamped by the release pipeline
testdata/                  sample workflows for integration tests
.github/workflows/         ci.yml (build+test on PR/push) + release.yml (cross-compile on tag)
scripts/                   install.sh (Linux/macOS) + install.ps1 (Windows)
```

All non-`main` code is under `internal/` — no public Go API. Dependency
direction is acyclic: `main` → `engine` → (`config`, `dag`, `scheduler`,
`runtime`, `workspace`, `envutil`, `log`, `logstore`); `scheduler` →
`dag`; `version` is leaf.

## Components

### `config` — schema & validation
- Parses with `gopkg.in/yaml.v3`, the only third-party dependency.
- **Strict decoding** (`dec.KnownFields(true)`): unknown keys — typos, or
  unsupported features like `uses`, `strategy`, `runs-on` — are rejected
  loudly rather than silently ignored.
- `needs` accepts both a scalar and a list via a custom `StringOrSlice`.
- `Validate()` aggregates *every* problem into one error: ≥1 job; each job
  has a `container` and ≥1 step; each step has a non-empty `run`; every
  `needs` target exists; no self-`needs`; the graph is acyclic.

### `dag` — ordering & waves
- Generic graph package — it knows nothing about jobs, so it cannot form an
  import cycle with `config`.
- `Build(deps)` returns a `Graph` with `Order` (flat topological order),
  `Needs` (defensive copy of input edges), `Dependents` (reverse edges),
  and `Indegree`. `Sort` is a thin wrapper that returns just `Order`.
- **Kahn's algorithm**; on a cycle returns a `*CycleError` naming the
  unresolved nodes.
- When several nodes are ready at once the alphabetically smallest is
  emitted first, so ordering is **deterministic** despite Go's randomized
  map iteration.
- `Waves(graph)` groups nodes into parallel execution waves — used by
  `-dry-run` to print the plan.

### `runtime` — container lifecycle
- Detection honors `LATCHET_RUNTIME`, else prefers `docker`, then `podman`.
  The detection core is a pure function with PATH lookup injected, so it is
  unit-tested without either binary installed.
- **One long-lived container per job; `exec` per step** (not `docker run`
  per step). The container idles on `sleep infinity`; each step `exec`s
  into it. Core speed decision (see rationale).
- Pull caching is owned by `engine` via an `imageCache` (sync.Once per
  image) so concurrent jobs sharing an image cause exactly one pull.
- Command builders (`createArgs`/`execArgs`/`rmArgs`/…) are pure functions
  returning argv, table-tested exhaustively.

### `workspace` — host directories (transient)
- One root per run under `<tmpdir>/latchet/<runid>/`; one sub-directory per
  job, bind-mounted to `/workspace`. `runid` is a sortable timestamp plus
  random suffix.
- Steps in a job share their directory; jobs do **not** share with each
  other.
- Removed on success; **kept on failure** with the path printed.
  `LATCHET_KEEP_WORKSPACE=1` forces retention; `LATCHET_WORKSPACE_ROOT`
  relocates the root.

### `logstore` — log files (persistent)
- A separate directory from the workspace, **outside `/tmp`**, so logs
  survive workspace cleanup. Location resolves in order: `LATCHET_LOG_DIR`,
  `$XDG_STATE_HOME/latchet`, `~/.local/state/latchet`.
- Per run: `<base>/<runid>/`; per job: `<base>/<runid>/<jobid>.log`. A
  `latest` symlink in `<base>/` points at the most recent run for
  convenience.
- The file always records the full output (markers + step output). For
  `-max-parallel=1`, the same writer is teed to stdout so the user sees
  streaming output as in v1.

### `envutil` — environment merge
- `Merge()` overlays workflow → job → step (increasing precedence) and
  returns a **sorted** `[]string` of `KEY=VALUE`, so generated container
  commands are deterministic.

### `scheduler` — parallel runner
- Owns parallel execution. A single controller goroutine + bounded worker
  pool sized by `MaxParallel` (the engine passes `runtime.NumCPU()` by
  default).
- Algorithm: seed `ready` with indegree-0 nodes (sorted alphabetically);
  dispatch while sem capacity allows; on each completion, decrement
  dependents' indegrees; synthesize skips for jobs whose needs didn't
  succeed.
- `MaxParallel=1` runs strictly sequentially through the same code path —
  no separate sequential implementation, no behavioral divergence.
- An infrastructure error from any worker cancels the shared context (which
  cancels in-flight `exec.CommandContext` calls), drains workers, marks
  every not-yet-launched job as skipped, and returns the error.
- Generic: takes a `RunJobFn` callback. No knowledge of containers or
  workspaces. Tested hermetically against fake `RunJobFn`s.

### `engine` — orchestration
- `Run(opts)` and `Validate(opts)` and `DryRun(opts)` — three entry points
  reading the same `Options` struct (`File`, `DryRun`, `MaxParallel`,
  `Stdout`, `Stderr`).
- Builds the dag.Graph, creates a workspace and a logstore.Run, then hands
  every job to the scheduler via a `RunJob` closure. The closure opens the
  job's log file, picks `stepW` (file only, or `MultiWriter(file, stdout)`
  for N=1), calls `runJob`, and emits terse `== job: X started/done ==`
  lines on stdout under parallel mode.
- Exit codes: `0` all succeeded · `1` a job failed · `2` config/parse error
  · `3` infrastructure error.

### `version` — build stamping
- Two `var`s, `Version` and `Commit`, defaulting to `"dev"` and
  `"unknown"`. The release pipeline overrides them via `go build -ldflags
  "-X .../version.Version=$TAG -X .../version.Commit=$SHA"`.

## Key decisions & rationale

- **Go, single static binary.** Lingua franca of CI/CD tooling; trivial
  distribution; `CGO_ENABLED=0` for a fully static build.
- **Shell out to the CLI, not the Docker SDK.** docker and podman share
  the subcommands latchet needs, so one code path serves both — and the
  dependency tree stays at exactly one third-party module. Cost: errors
  must be read from exit codes / stderr text rather than a typed API.
- **One container per job, `exec` per step.** `docker run` per step would
  pay container create + teardown on every step; `exec` into a warm
  container is near-instant and keeps the filesystem cache hot.
- **`sh -c`, not `bash`.** `sh` exists in virtually every base image;
  `bash` does not. `set -e` is prepended to each `run` so a failing line
  fails the step. Cost: no bash-isms (a configurable `shell:` is a
  roadmap item).
- **Parallel by default, capped at `NumCPU`.** Independent jobs run
  concurrently; concurrency reaching `NumCPU` is enough to saturate even
  large workflows on a workstation, with predictable resource use.
  `-max-parallel=1` falls back to v1-style sequential streaming.
- **Per-job log files in a persistent directory.** Solves parallel output
  interleaving without the readability tax of per-line prefixing — each
  job's log is one contiguous block in a file you can `tail -f` or
  `grep`. Logs outlive workspaces so successful runs are inspectable.
- **Stdlib `flag`, no CLI framework.** Six flags, no need for cobra or
  pflag. Keeps the dependency tree at exactly one third-party module.
- **Empty workspaces.** Jobs start with an empty `/workspace` and script
  their own checkout — matching GitHub Actions' explicit-checkout model.

## Build & release

- **CI** (`.github/workflows/ci.yml`): `go build ./...`, `go vet ./...`,
  `go test ./...` on PR + main pushes. Integration tests are NOT in CI
  (they need a container runtime); run them locally with `go test -tags
  integration`.
- **Releases** (`.github/workflows/release.yml`): on a `v*` tag, a single
  Ubuntu runner cross-compiles six targets (linux/macOS/Windows ×
  amd64/arm64) in a shell loop, packages each as a tar.gz or zip, writes
  `SHA256SUMS`, and creates a GitHub release with `gh release create
  --generate-notes`. `workflow_dispatch` is enabled for pre-tag smoke
  tests; it builds artifacts but skips the release-create step.
- **Install scripts** (`scripts/install.sh`, `scripts/install.ps1`) query
  the GitHub Releases API, verify SHA256, and install into a user-
  writable directory (with PATH guidance on stdout).

## Testing

- **Unit tests** (`go test ./...`) need no container runtime and are
  fast: `config` parsing/validation, `dag` ordering/waves/cycles,
  `envutil` precedence, `runtime` command builders and detection,
  `scheduler` ordering/parallelism/failure-propagation, `logstore`
  base-dir resolution and file creation.
- **Integration tests** behind `//go:build integration` run real
  workflows against a real container runtime:
  - `TestRunSampleWorkflow` — `testdata/latchet.yml` end-to-end.
  - `TestParallelSpeedup` — `testdata/parallel.yml` at `MaxParallel=1`
    vs `MaxParallel=3`, asserting parallel ≤ 60% of sequential.
  Run with `go test -tags integration ./internal/engine/...`.
