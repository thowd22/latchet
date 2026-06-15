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
`version` is leaf. `dag` and `scheduler` are deliberately generic (know nothing
about jobs/containers) so they can't form import cycles and are tested
hermetically.

## Conventions that matter here

- **Strict YAML.** Unknown keys (`uses`, `strategy`, `runs-on`, …) are rejected
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
`LATCHET_LOG_DIR`, `LATCHET_COSIGN_KEY`, `LATCHET_COSIGN_TLOG`. Distinct from
the output-only `LATCHET_*` vars `builtinenv` *injects* into steps
(`LATCHET_WORKSPACE`, `LATCHET_RUN_ID`, `LATCHET_JOB_ID`, `LATCHET_GIT_*`).

Each run emits `<logdir>/provenance.json` (SLSA v1.0, `internal/provenance`),
optionally signed via cosign (`internal/signer`) when `LATCHET_COSIGN_KEY` is
set. Both are best-effort and never change the exit code. `latchet verify
<provenance.json>` (`engine.Verify`) re-derives a run and compares subjects
(strict/lax), writing `verification.json`.
