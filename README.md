# latchet

A minimal, fast Docker/Podman-based CI/CD workflow engine.

> *latchet* — an old word for a fastening or latch; the piece that holds things secure.

`latchet` reads a single YAML file, `latchet.yml`, whose syntax closely follows a
small subset of GitHub Actions. It runs each job inside a container, in dependency
order (in parallel by default), and writes per-job log files.

## Install

```sh
# Linux / macOS
curl -sSL https://raw.githubusercontent.com/thowd22/latchet/main/scripts/install.sh | sh

# Windows (PowerShell)
iwr -useb https://raw.githubusercontent.com/thowd22/latchet/main/scripts/install.ps1 | iex
```

Pin a version with `LATCHET_VERSION=v0.2.0` / `$env:LATCHET_VERSION='v0.2.0'`.
Pick a custom install dir with `LATCHET_INSTALL_DIR` / `$env:LATCHET_INSTALL_DIR`.

Or build from source: `go build -o latchet ./cmd/latchet`.

## Usage

With no flags, `latchet` reads `./latchet.yml` and runs every job:

```sh
latchet                                       # ./latchet.yml, jobs parallel up to NumCPU
latchet -file ci/release.yml                  # alternate workflow file
latchet -max-parallel 1                       # sequential; step output streamed to stdout
latchet -dry-run                              # print execution plan and exit
latchet -validate-only                        # parse + validate and exit
latchet -version                              # print version and exit
latchet -help                                 # full usage
```

Exit codes:

| Code | Meaning |
|------|---------|
| `0`  | every job succeeded |
| `1`  | a job failed |
| `2`  | `latchet.yml` is missing or invalid (or bad flags) |
| `3`  | a container/runtime/workspace operation failed |

## Workflow syntax

```yaml
name: demo
env:                          # workflow-level env (lowest precedence)
  GLOBAL: workflow-level
jobs:
  build:
    container: alpine:3.19     # image the job's steps run inside
    env:
      STAGE: build             # job-level env (overrides workflow)
    steps:
      - name: write artifact
        run: echo "hello from $STAGE ($GLOBAL)" > out.txt
      - name: show artifact
        run: cat out.txt
  test:
    container: alpine:3.19
    needs: build               # scalar or list: needs: [build, lint]
    inherit: build             # copy build's /workspace before test starts
    steps:
      - name: run tests
        env:
          ONLY: step-level     # step-level env (highest precedence)
        run: echo "testing"
```

- **Jobs** run in topological order of `needs`. Independent jobs run **in
  parallel** by default (cap with `-max-parallel`).
- A failing step fails its job; jobs that depend on a failed/skipped job
  are skipped.
- **Steps** run via `sh -c` with `set -e` prepended. Steps in a job share
  a `/workspace` directory; jobs do not share with each other by default
  (see `inherit:` below for one-parent file sharing).
- A job may declare `inherit: <parent-id>` (which must also appear in
  `needs:`) to start with the parent's `/workspace` files copied in.
  Single parent only; named-artifact upload/download is not yet supported.
- `env` merges workflow → job → step, highest precedence last.
- Unknown keys (`uses`, `strategy`, `runs-on`, ...) are rejected — they
  are not supported.

## Output and logs

Every run writes a full log file per job to a persistent directory:

```
~/.local/state/latchet/<runid>/<jobid>.log
```

(Override with `LATCHET_LOG_DIR`, or use `$XDG_STATE_HOME/latchet/<runid>/`
when `XDG_STATE_HOME` is set.) A `latest` symlink in that directory points
at the most recent run. Logs survive workspace cleanup, so successful-run
output is preserved for inspection.

Stdout behavior depends on `-max-parallel`:
- `-max-parallel 1` — full step output streams live to stdout *and* the log
  file. Matches v1's UX.
- Parallel (default) — stdout shows job start/end markers and the log path
  for each job; full step output goes only to the log file (so concurrent
  jobs don't interleave).

## Sharing files between jobs

A job may declare `inherit: <parent-id>` to start with the named parent's
`/workspace` copied in. The parent must also appear in `needs:` so the
graph stays self-documenting. Regular files, directories (including empty
ones), and symlinks (preserved verbatim, not followed) are copied; mode
bits are preserved. Special files (devices, sockets, fifos) abort the run
as an infra error. Single-parent only — multi-parent merge semantics and
named-artifact upload/download (`actions/upload-artifact`-style) are
deferred (see ROADMAP).

## Container runtime

`latchet` shells out to `docker` or `podman` (auto-detected; `docker`
preferred). Override with `LATCHET_RUNTIME=podman`.

## Environment variables

| Variable | Effect |
|----------|--------|
| `LATCHET_RUNTIME` | force `docker` or `podman` |
| `LATCHET_WORKSPACE_ROOT` | where run workspaces are created (default `<tmp>/latchet`) |
| `LATCHET_KEEP_WORKSPACE=1` | keep the workspace even on success |
| `LATCHET_LOG_DIR` | base directory for log files (default per XDG / `~/.local/state/latchet`) |

A failed run always keeps its workspace and prints the path.

## Documentation

- [`docs/design.md`](docs/design.md) — architecture and design rationale
- [`ROADMAP.md`](ROADMAP.md) — deferred features

## Development

```sh
go test ./...                              # fast unit tests, no runtime needed
go test -tags integration ./internal/...   # runs sample workflows on a real runtime
```
