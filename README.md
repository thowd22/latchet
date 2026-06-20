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

### Verifying a release

Release artifacts are **keyless-signed** by the release pipeline: GitHub's OIDC
identity mints a short-lived [Fulcio](https://docs.sigstore.dev/) certificate
(no key material on disk) and the signature is recorded in the
[Rekor](https://docs.sigstore.dev/logging/overview/) transparency log. Each
release ships a `SHA256SUMS` plus a `SHA256SUMS.bundle`. Verify the checksums
file's signature, then check your download against it:

```sh
cosign verify-blob \
  --bundle SHA256SUMS.bundle \
  --certificate-identity-regexp '^https://github\.com/thowd22/latchet/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS    # verify your downloaded artifact
```

A passing `cosign verify-blob` proves the checksums were produced by this
repository's release workflow (SLSA Build L2 — a hosted builder with signed
provenance). Requires **cosign v3+**.

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
- **Multi-line `run:`** uses a YAML literal block (`|`). The whole block is one
  shell script (one `sh -c`), so state persists *within* a step but not between
  steps; with `set -e`, the first failing line aborts the step. It is POSIX
  `sh`, not bash — avoid bash-isms.

  ```yaml
  steps:
    - name: build and test
      run: |
        mkdir -p out && cd out        # cd persists within this step
        go build -o app ../cmd/app
        ./app --version
        grep -q ok results.txt || echo "no results"   # tolerate non-zero with ||
  ```
- **Conditional steps** — a step may carry `if:` / `elif:` / `else: true`; see
  [Run location and conditional steps](#run-location-and-conditional-steps).
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
| `LATCHET_LOCATION` | where the run is executing (e.g. `server` vs `local`); see [Run location](#run-location-and-conditional-steps) |
| `LATCHET_ENV` | path to the step-output file; append `NAME=value` to it to set [step outputs](#step-outputs) |

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

Values of declared [secrets](#secrets) are redacted from `internalParameters`
(recorded as `***`). Note that any env value written *inline* in the workflow's
`env:` is recorded as-is — put credentials in `secrets:`, not `env:`.

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

## Secrets

Declare credentials with `secrets:` — a list of **host environment variable
names** (workflow- and/or job-level). For each name that is set in latchet's
own environment, the value is injected into that job's steps *and* masked
everywhere latchet writes it:

```yaml
name: deploy
secrets: [REGISTRY_TOKEN]      # applies to every job
jobs:
  publish:
    container: alpine:3.19
    secrets: [EXTRA_TOKEN]     # job-local; unioned with the workflow list
    steps:
      - run: echo "logging in with $REGISTRY_TOKEN"   # prints: logging in with ***
```

- **Source from the host, not the YAML.** A secret's value comes from
  `$REGISTRY_TOKEN` in latchet's environment — never write the value in the
  file. (Anything placed directly in `env:` is *not* a secret and is logged and
  recorded as-is.)
- **Masked in logs.** Any occurrence of a secret value in step output (and the
  per-job log file) is replaced with `***`, even when split across output
  chunks.
- **Redacted in provenance.** Secret values are recorded as `***` in
  `provenance.json` (see [Provenance](#provenance-slsa)).
- A declared name that is unset in the host environment is simply skipped.

> Masking is substring-based, so avoid declaring a secret whose value is a
> short common string — it would mask unrelated output.

## Run location and conditional steps

`LATCHET_LOCATION` tells a run *where* it's executing, so a workflow can behave
differently on the latchet server vs a developer's laptop (e.g. only deploy
from the server). It is **machine-scoped**, set in the global
[`latchet-ci.yml`](#global-configuration) — a per-project `latchet.yml` is the
same on every machine and can't distinguish them:

```yaml
# the latchet server's ~/.config/latchet/latchet-ci.yml
location: server
```

Resolution is `LATCHET_LOCATION` env var → global config `location:` →
`local` (default). The value is injected as the `LATCHET_LOCATION` built-in
step var. `latchet watch` runs (which execute on the server) inherit it
automatically.

### Conditional steps (`if` / `elif` / `else`)

A step may carry a condition. `if:` starts a chain, `elif:` continues it, and
`else: true` is the fallback — the **first** branch whose condition is true
runs; the others are skipped (and logged as skipped). A plain step (no
condition) always runs and ends any open chain.

```yaml
steps:
  - run: make build                  # always runs
  - if: $LATCHET_LOCATION == server
    run: ./deploy prod
  - elif: $LATCHET_LOCATION == staging
    run: ./deploy staging
  - else: true
    run: echo "no deploy on $LATCHET_LOCATION"
```

Conditions are a small boolean language evaluated by latchet (not the shell)
against the step's merged env:

- **Values:** `$VAR` / `${VAR}` expand from the env (missing → empty); bare
  words and `'…'`/`"…"` are literals.
- **Operators:** `==`, `!=`, `&&`, `||`, `!`, and parentheses. A lone value is
  truthy when non-empty and not `false`/`0`.

```yaml
  - if: $LATCHET_LOCATION == server && $LATCHET_GIT_BRANCH == main
    run: ./release
```

Invalid `if:`/`elif:` expressions and malformed chains (an `elif`/`else` with no
preceding `if`) are rejected at load time (exit code `2`). To skip an entire
*job* by condition, put `if:` on the **job** instead — when false the whole job
is skipped, and (like a `needs`-skip) its dependents are skipped too:

```yaml
jobs:
  deploy:
    container: alpine:3.19
    if: $LATCHET_LOCATION == server   # whole job skipped unless on the server
    steps:
      - run: ./deploy.sh
```

A job `if:` is evaluated before the job starts, against the same env the job's
first step would see (built-ins, `needs` outputs, workflow/job env, secrets) —
but not step outputs (no step has run yet). Jobs take a single `if:` (no
`elif`/`else`, since jobs form a dependency graph, not an ordered chain).

## Step outputs

A step can hand a value to **later steps in the same job** by appending
`NAME=value` lines to the file at `$LATCHET_ENV` (a built-in path). After each
step, latchet reads that file and injects the values as plain env vars into the
remaining steps — no templating, just `$NAME`:

```yaml
steps:
  - name: derive version
    run: echo "VERSION=$(cat VERSION)" >> "$LATCHET_ENV"
  - name: build
    run: docker build -t app:$VERSION .   # VERSION is a normal env var here
  - name: tag latest only on a release
    if: $VERSION != ''                     # outputs work in conditions too
    run: echo "tagging $VERSION"
```

- Outputs sit **above** workflow/job env but a later step's own `env:` still
  wins; if two steps set the same name, the last one wins.
- Each `NAME` must be a valid env-var name (`[A-Za-z_][A-Za-z0-9_]*`); values are
  single-line. Lines without `=` or with an invalid name are ignored.
- Each `NAME` must be a valid env-var name (`[A-Za-z_][A-Za-z0-9_]*`); values are
  single-line. Lines without `=` or with an invalid name are ignored.

### Passing outputs to other jobs

A job can export selected outputs to the jobs that depend on it. Declare the
names under `outputs:`; latchet injects those values as env vars into every job
that lists this one in `needs:`:

```yaml
jobs:
  build:
    container: golang:1.22
    outputs: [VERSION]                      # only these cross to dependents
    steps:
      - run: |
          git clone --depth 1 "$LATCHET_GIT_URL" .
          echo "VERSION=$(git describe --tags --always)" >> "$LATCHET_ENV"
  release:
    container: alpine:3.19
    needs: build
    steps:
      - run: echo "releasing $VERSION"      # from build's outputs
```

- Only names listed in `outputs:` cross to dependents — other values a step set
  stay within the producing job.
- A dependent reads them as plain env vars (above workflow/job env, below its
  own step `env:`); if two dependencies export the same name, the later one in
  `needs:` wins.
- A declared output that was never set crosses as an empty string (with a
  warning in the producer's log).
- This passes *values*; to pass *files* between jobs use `inherit:` (see below).

> latchet reserves the `/workspace/.latchet/` directory for the output file; it
> is not recorded as a provenance artifact.

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
| `LATCHET_CONFIG` | explicit path to the [global config](#global-configuration) (overrides the default search) |

A failed run always keeps its workspace and prints the path.

## Global configuration

An optional machine-wide config, `latchet-ci.yml`, sets user defaults — it is
separate from the per-project workflow `latchet.yml`. With no file present,
latchet behaves exactly as without it. It is loaded from the first of:

1. `$LATCHET_CONFIG` (explicit path)
2. `$XDG_CONFIG_HOME/latchet/latchet-ci.yml`
3. `~/.config/latchet/latchet-ci.yml`

```yaml
runtime: podman                 # preferred container runtime
workspace_root: /var/lib/latchet/ws
log_dir: /var/log/latchet
max_parallel: 4                 # default job concurrency
location: server                # injected as LATCHET_LOCATION (default "local")
env:                            # default env merged into every run
  REGISTRY: ghcr.io/me
watch:                          # repositories for `latchet watch`
  - url: git@github.com:me/app.git
    branches: [main]
    tags: ["v*"]
```

Precedence — **CLI flags > environment variables > global config > built-in
defaults**. So `runtime`/`workspace_root`/`log_dir` fill the matching
`LATCHET_*` env var only when it is unset, `max_parallel` applies unless
`-max-parallel` was passed, and the `env:` map is merged **below** a workflow's
own `env:` (a workflow always overrides a machine default). Unknown keys are
rejected, as in `latchet.yml`.

## Watching repositories (`latchet watch`)

`latchet watch` turns latchet into a minimal CI server. It does **one pass**
over the repositories in the global config's `watch:` list and exits — there is
no internal timer, so you schedule it with cron:

```cron
*/5 * * * * latchet watch        # every 5 minutes
```

Each pass runs `git ls-remote` on every watched repo and, when a configured
**branch** has advanced or a **tag** matching a pattern (`v*`, `v1.0.0`, …) has
appeared or moved, it clones that commit and runs the repo's `latchet.yml`.

- **Fires exactly once per change.** Last-seen SHAs are kept in
  `$XDG_STATE_HOME/latchet/watch/state.json` (override with
  `LATCHET_WATCH_STATE`). The **first** pass for a repo (or a newly-added tag
  pattern) records a baseline without firing.
- **Branches and tags only** — no PR/MR triggers (see the [discover PRs/MRs
  roadmap item](ROADMAP.md#prebuilt-actions--build-steps)). The intended
  transport is **SSH** (your existing key; latchet does no token handling),
  though latchet shells out to `git` and accepts any URL it resolves.
- **Trust:** a fired run executes whatever `latchet.yml` the remote ships, in
  the usual job containers — only watch repositories you trust.
- A repo with no `latchet.yml`, or a `git` error on one repo, is logged and
  skipped without aborting the pass.

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
