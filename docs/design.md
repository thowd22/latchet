# latchet — Design

This document records how `latchet` v1 is built and *why*. For what it does and
how to use it, see [`README.md`](../README.md); for what comes next, see
[`ROADMAP.md`](../ROADMAP.md).

## Overview

`latchet` is a minimal, fast Docker/Podman-based CI/CD workflow engine: a single
Go binary that reads one `latchet.yml` file (a small subset of GitHub Actions
syntax), runs each job inside a container in dependency order, and streams the
output. v1 has no UI and no CLI flags — it reads `./latchet.yml` and runs it.

**v1 scope (GitHub Actions subset):** top-level `name` + `jobs`; per-job
`container`, `env`, `needs`, `steps`; per-step `name`, `run`, `env`; `env`
merging across the three levels. Everything else is rejected at parse time.

## Project layout

```
cmd/latchet/main.go        entry: locate ./latchet.yml, run engine, set exit code
internal/
  config/                  YAML schema, strict parsing, aggregated validation
  dag/                     Kahn topological sort + cycle detection
  envutil/                 ordered env merge (workflow -> job -> step)
  workspace/               per-run/per-job host directories, cleanup policy
  runtime/                 docker/podman detection + container lifecycle
  log/                     plain-stdout job/step markers + run summary
  engine/                  orchestrator tying it all together
testdata/latchet.yml       sample workflow used by the integration test
```

All non-`main` code is under `internal/` — v1 exposes no public API. Dependency
direction is acyclic: `main` → `engine` → (`config`, `dag`, `runtime`,
`workspace`, `envutil`, `log`).

## Components

### `config` — schema & validation
- Parses with `gopkg.in/yaml.v3`, the only third-party dependency.
- **Strict decoding** (`dec.KnownFields(true)`): unknown keys — typos, or
  unsupported features like `uses`, `strategy`, `runs-on` — are rejected loudly
  rather than silently ignored.
- `needs` accepts both a scalar and a list via a custom `StringOrSlice` type.
- `Validate()` aggregates *every* problem into one error, not just the first:
  ≥1 job; each job has a `container` and ≥1 step; each step has a non-empty
  `run`; every `needs` target exists; no self-`needs`; the graph is acyclic.

### `dag` — ordering
- A generic `Sort(deps map[string][]string)` — it knows nothing about jobs, so
  it cannot form an import cycle with `config`.
- **Kahn's algorithm**: produces the execution order, and on a cycle returns a
  `*CycleError` naming the unresolved nodes.
- When several nodes are ready at once the alphabetically smallest is emitted
  first, so ordering is **deterministic** despite Go's randomized map iteration.

### `runtime` — container lifecycle
- Detection honors `LATCHET_RUNTIME`, else prefers `docker`, then `podman`. The
  detection core is a pure function with PATH lookup injected, so it is unit-
  tested without either binary installed.
- **One long-lived container per job; `exec` per step** (not `docker run` per
  step). The container idles on `sleep infinity`; each step `exec`s into it.
  This is the core speed decision — see rationale below.
- Pull caching: `image inspect` first, `pull` only on miss; each image is
  pulled at most once per run.
- Command builders (`createArgs`/`execArgs`/`rmArgs`/…) are pure functions
  returning argv, table-tested exhaustively.

### `workspace` — host directories
- One root per run under `<tmpdir>/latchet/<runid>/`; one sub-directory per job,
  bind-mounted to `/workspace`. `runid` is a sortable timestamp + random suffix.
- Steps in a job share their directory; jobs do **not** share with each other.
- Removed on success; **kept on failure** with the path printed.
  `LATCHET_KEEP_WORKSPACE=1` forces retention; `LATCHET_WORKSPACE_ROOT`
  relocates the root.

### `envutil` — environment merge
- `Merge()` overlays workflow → job → step (increasing precedence) and returns
  a **sorted** `[]string` of `KEY=VALUE`, so generated container commands are
  deterministic.

### `engine` — orchestration
- `Run(path)`: load → validate → detect runtime → order jobs → run each → emit
  a summary → return an exit code.
- A failed step fails its job immediately; jobs that (transitively) depend on a
  failed or skipped job are themselves marked `skipped`.
- Exit codes: `0` all succeeded · `1` a job failed · `2` config/parse error ·
  `3` infrastructure error (no runtime, image pull, container op).

## Key decisions & rationale

- **Go, single static binary.** Lingua franca of CI/CD tooling; trivial
  distribution; built with `CGO_ENABLED=0`.
- **Shell out to the CLI, not the Docker SDK.** docker and podman share the
  subcommands latchet needs, so one code path serves both — and the dependency
  tree stays at exactly one third-party module. Cost: errors must be read from
  exit codes / stderr text rather than a typed API.
- **One container per job, `exec` per step.** `docker run` per step would pay
  container create + teardown on every step; `exec` into a warm container is
  near-instant and keeps the filesystem cache hot. This is what makes latchet
  "fast."
- **`sh -c`, not `bash`.** `sh` exists in virtually every base image; `bash`
  does not. `set -e` is prepended to each `run` so a failing line fails the
  step. Cost: no bash-isms in v1 (a configurable `shell:` is a v2 item).
- **Sequential job execution.** Simplest correct approach and it keeps streamed
  logs linear and un-interleaved. The DAG already exposes the graph, so adding
  parallelism in v2 is additive, not a rewrite.
- **Empty workspaces.** Jobs start with an empty `/workspace` and script their
  own checkout — matching GitHub Actions' explicit-checkout model.

## Testing

- **Unit tests** (`go test ./...`) need no container runtime and are fast:
  `config` parsing/validation, `dag` ordering and cycle detection, `envutil`
  precedence, `runtime` command builders and detection.
- **Integration test** behind the `//go:build integration` tag runs
  `testdata/latchet.yml` against a real runtime: `go test -tags integration
  ./internal/engine/...`. Gated so the default test run stays hermetic.
