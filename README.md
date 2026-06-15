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
      - name: built-in vars            # LATCHET_* injected automatically
        run: echo "run $LATCHET_RUN_ID, job $LATCHET_JOB_ID, sha $LATCHET_GIT_SHA"
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
- `env` merges built-in → workflow → job → step, highest precedence last
  (see [Built-in step variables](#built-in-step-variables)).
- Unknown keys (`uses`, `strategy`, `runs-on`, ...) are rejected — they
  are not supported.

## Checking out your code

latchet does **not** check out your repository for you. Every job starts
with an **empty** `/workspace`, and there is no `actions/checkout`
equivalent (`uses` is unsupported). If a job needs your source, clone it
yourself as the first step:

```yaml
jobs:
  test:
    container: golang:1.22
    steps:
      - name: checkout
        run: git clone --depth 1 "$LATCHET_GIT_URL" .
      - run: go test ./...
```

`$LATCHET_GIT_URL` is injected automatically (see [Built-in step
variables](#built-in-step-variables)); pin a commit with `$LATCHET_GIT_SHA`
if you need the exact revision latchet recorded. To avoid re-cloning in
every downstream job, hand a checked-out workspace to a child job with
`inherit:` (see [Sharing files between jobs](#sharing-files-between-jobs)).

## Built-in step variables

latchet injects a set of `LATCHET_*` variables into every step before your
own `env:` merges on top — so they are available everywhere and can be
overridden (or faked for testing) by any `env:` you declare. The `LATCHET_`
prefix keeps them from colliding with workflow- or image-defined variables.

| Variable | Value |
|----------|-------|
| `LATCHET_WORKSPACE` | container-side workspace path (always `/workspace`) |
| `LATCHET_RUN_ID` | this run's id (matches the workspace and log dir name) |
| `LATCHET_JOB_ID` | the current job's id |
| `LATCHET_GIT_URL` | origin remote URL of the host checkout |
| `LATCHET_GIT_BRANCH` | current branch (empty in detached HEAD) |
| `LATCHET_GIT_TAG` | tag name when HEAD is exactly a tag, else empty |
| `LATCHET_GIT_SHA` | full commit SHA of HEAD |
| `LATCHET_GIT_REF` | full ref, e.g. `refs/heads/main` or `refs/tags/v1.0.0` |

The `LATCHET_GIT_*` values are read from the host working directory via `git`
(best-effort: empty strings when run outside a git checkout or with no `git`
on `PATH`). These are *output* variables injected into steps, distinct from
the *input* variables in [Environment variables](#environment-variables) that
configure latchet itself.

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

## Provenance (SLSA)

Every executed run writes a `provenance.json` next to its logs:

```
~/.local/state/latchet/<runid>/provenance.json
```

It is an [in-toto](https://github.com/in-toto/attestation) statement carrying
a [SLSA v1.0](https://slsa.dev/spec/v1.0/provenance) provenance predicate, so
**every latchet run is SLSA Build L1 with no extra configuration**. It records:

- **subject[]** — SHA256 of each file left under a job's `/workspace`
  (named `<jobid>/<path>`). A run with no file artifacts attests the
  workflow file itself.
- **resolvedDependencies** — each container image pinned to the digest it
  actually resolved to at pull time (`…/golang@sha256:…`).
- **externalParameters** — the workflow file path + its SHA256, the
  invocation flags, and the source git ref/revision.
- **internalParameters** — per-job image (as written) and per-step `run`
  strings and merged env.
- **builder / metadata** — latchet version+commit, run id, and start/finish
  timestamps.

Emission is best-effort and never changes a run's exit code. It is written
for failed runs too (a faithful record), but not when a run aborts on an
infrastructure error.

> ⚠️ **Secret values are not yet redacted.** Until secret masking lands,
> `internalParameters` records merged env *values* in plaintext. Treat the
> log directory accordingly; don't publish `provenance.json` from a run whose
> env carried secrets.

### Signing the attestation

Set `LATCHET_COSIGN_KEY` to a [cosign](https://docs.sigstore.dev/) private key
and, when `cosign` is on `PATH`, latchet signs the provenance after writing
it, producing a Sigstore bundle `provenance.json.bundle` alongside it:

```sh
COSIGN_PASSWORD=… LATCHET_COSIGN_KEY=cosign.key latchet
# -> latchet: provenance signed -> …/provenance.json.bundle
```

cosign reads the key's password from `COSIGN_PASSWORD` (its own variable).
Signing is **offline by default** (`--tlog-upload=false`); set
`LATCHET_COSIGN_TLOG=1` to also publish the signature to a Rekor transparency
log. cosign is a **soft dependency**: if it's missing, or no key is
configured, the run continues and the attestation is simply left unsigned —
signing never changes a run's exit code. Verify the bundle with:

```sh
cosign verify-blob --key cosign.pub \
  --bundle provenance.json.bundle \
  --insecure-ignore-tlog=true provenance.json
```

Requires **cosign v3+** (the offline key-based flow uses
`--use-signing-config=false`). Keyless signing (Fulcio/OIDC, no key on disk)
is the intended path for the release pipeline running in GitHub Actions; local
runs use the key-based flow above so they work unattended.

### Verifying a run

`latchet verify` re-derives a build from its provenance and compares the
result — so anyone can independently re-run someone else's claimed build:

```sh
latchet verify provenance.json              # lax (default)
latchet verify --strict provenance.json     # require bit-for-bit match
latchet verify --explain provenance.json    # print per-subject mismatch detail
latchet verify --file latchet.yml provenance.json   # override workflow path
latchet verify --key cosign.pub provenance.json     # also verify the signature
```

With `--key`, latchet first verifies the manifest's signature bundle
(`provenance.json.bundle`, from [signing](#signing-the-attestation)) with the
given cosign public key and fails fast if it doesn't check out — so a tampered
or unsigned manifest is rejected before any re-run.

It (1) checks the on-disk workflow's SHA256 matches the manifest — you can't
reproduce a build from a different recipe; (2) re-runs the workflow in a fresh
workspace with each image **pinned to the digest** recorded in
`resolvedDependencies`; and (3) re-hashes the artifacts and compares them to
the manifest's subjects, writing a `verification.json` report.

- **lax** (default) — passes when every claimed subject is reproduced *by
  name*; differing content is reported as a warning. Honest for the common
  case where builds are only partially reproducible.
- **`--strict`** — every subject must match bit-for-bit; any mismatch, missing,
  or extra subject fails. Only meaningful for fully-reproducible workflows.

Exit codes: `0` verified · `1` verification failed · `2` bad
manifest/workflow · `3` runtime error.

> Most real workflows aren't bit-for-bit reproducible (timestamps, embedded
> build paths, VCS metadata), so `--strict` is for workflows deliberately made
> deterministic. `--explain` lists expected-vs-actual hashes; true byte-level
> diffing of artifacts is out of scope (the manifest records hashes, not the
> original bytes).

### Reproducible builds (determinism helpers)

To make more of a workflow's output reproducible (and so verifiable under
`--strict`), opt into the determinism helpers — set `deterministic: true` at
the workflow or job level, or `LATCHET_DETERMINISTIC=1`:

```yaml
name: release
deterministic: true            # applies to every job
jobs:
  build:
    container: golang:1.22
    # deterministic: true      # or just this one job
    steps:
      - run: git clone --depth 1 "$LATCHET_GIT_URL" .
      - run: go build -trimpath -o app ./...
```

When on, latchet injects these at built-in (lowest) precedence, so a workflow
can still override any of them:

| Variable | Value |
|----------|-------|
| `SOURCE_DATE_EPOCH` | HEAD's commit time (Unix seconds) — stable across re-runs of the same commit; falls back to run-start time when unavailable |
| `LC_ALL`, `LANG` | `C` |
| `TZ` | `UTC` |

This is **best-effort triage, not a guarantee**: reproducibility ultimately
lives in the toolchain and the workflow's discipline. Pair it with
`SOURCE_DATE_EPOCH`-aware tools (Go `-trimpath`, recent npm, `cargo`, `gcc
-ffile-prefix-map`) and deterministic archiving (`tar --sort=name
--mtime=@$SOURCE_DATE_EPOCH`). Hermetic builds (Nix/Bazel-grade) are out of
scope.

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
| `LATCHET_COSIGN_KEY` | path to a cosign private key; when set (and `cosign` is on `PATH`), the run's `provenance.json` is signed (see [Provenance](#provenance-slsa)) |
| `LATCHET_COSIGN_TLOG=1` | also upload the signature to a Rekor transparency log (off by default, so signing works offline) |
| `LATCHET_DETERMINISTIC=1` | force the [determinism helpers](#reproducible-builds-determinism-helpers) on for every job |

A failed run always keeps its workspace and prints the path.

## Limitations

- **No dependency cache between jobs.** Each job runs in its own container
  with a fresh `/workspace`, so package/build caches (Go modules, npm,
  pip, …) are not shared — every job re-downloads its dependencies. Warm a
  cache within a single job, or hand artifacts to a child job with
  `inherit:`. A shared cache mount is on the [roadmap](ROADMAP.md).
- **No implicit checkout** — clone your repo yourself (see [Checking out
  your code](#checking-out-your-code)).

## Documentation

- [`docs/design.md`](docs/design.md) — architecture and design rationale
- [`ROADMAP.md`](ROADMAP.md) — deferred features

## Development

```sh
go test ./...                              # fast unit tests, no runtime needed
go test -tags integration ./internal/...   # runs sample workflows on a real runtime
```
