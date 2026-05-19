# latchet-ci

A minimal, fast Docker/Podman-based CI/CD workflow engine.

> *latchet* — an old word for a fastening or latch; the piece that holds things secure.

`latchet` reads a single YAML file, `latchet.yml`, whose syntax closely follows a
small subset of GitHub Actions. It runs each job inside a container, in dependency
order, and streams the output.

## Usage

Put a `latchet.yml` in a directory and run the binary there — it takes no arguments:

```sh
go build -o latchet ./cmd/latchet
./latchet
```

It reads `./latchet.yml` from the current working directory and exits with:

| Code | Meaning |
|------|---------|
| `0`  | every job succeeded |
| `1`  | a job failed |
| `2`  | `latchet.yml` is missing or invalid |
| `3`  | a container/runtime/workspace operation failed |

## Workflow syntax

```yaml
name: demo
env:                       # workflow-level env (lowest precedence)
  GLOBAL: workflow-level
jobs:
  build:
    container: alpine:3.19  # image the job's steps run inside
    env:
      STAGE: build          # job-level env (overrides workflow)
    steps:
      - name: write artifact
        run: echo "hello from $STAGE ($GLOBAL)" > out.txt
      - name: show artifact
        run: cat out.txt
  test:
    container: alpine:3.19
    needs: build            # scalar or list: needs: [build, lint]
    steps:
      - name: run tests
        env:
          ONLY: step-level   # step-level env (highest precedence)
        run: echo "testing"
```

- **Jobs** run in topological order of their `needs` edges (sequentially in v1).
- A failing step fails its job; jobs that depend on a failed/skipped job are skipped.
- **Steps** run via `sh -c` with `set -e` prepended. Steps in a job share a
  `/workspace` directory; jobs do not share with each other.
- `env` merges workflow → job → step, highest precedence last.
- Unknown keys (`uses`, `strategy`, `runs-on`, ...) are rejected — they are not
  supported in v1.

## Container runtime

`latchet` shells out to `docker` or `podman` (auto-detected; `docker` preferred).
Override with `LATCHET_RUNTIME=podman`.

## Environment variables

| Variable | Effect |
|----------|--------|
| `LATCHET_RUNTIME` | force `docker` or `podman` |
| `LATCHET_WORKSPACE_ROOT` | where run directories are created (default `<tmp>/latchet`) |
| `LATCHET_KEEP_WORKSPACE=1` | keep the workspace even on success |

A failed run always keeps its workspace and prints the path.

## Development

```sh
go test ./...                              # fast unit tests, no runtime needed
go test -tags integration ./internal/...   # runs the sample workflow on a real runtime
```
