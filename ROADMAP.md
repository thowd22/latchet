# latchet — Roadmap

v1 is a deliberately minimal workflow engine (see [`docs/design.md`](docs/design.md)
for what shipped and why). The items below were consciously deferred to keep v1
small. They are *deferrals*, not designed features — each still needs a design
pass before implementation.

## v2 candidates

### High impact
- **`uses` / reusable actions** — run pre-packaged steps from another repo.
  The largest item: needs action fetching/resolution and a substantially bigger
  runner. This is what would make latchet workflows reusable rather than purely
  inline.
- **Parallel job execution** — run jobs whose `needs` are all satisfied
  concurrently instead of sequentially. The DAG already exposes the graph, so
  this is additive; the main work is per-job log prefixing/buffering so streams
  don't interleave. *Lowest-effort high-value item — natural first pick.*
- **Named artifacts (`upload-artifact` / `download-artifact`-style)** — pass
  selected files between arbitrary jobs by name. v3 added single-parent
  workspace inheritance (`inherit: <jobid>`) covering the common
  parent-to-children case; this item covers the harder cases: many-to-many,
  fan-in merges, exclude patterns, and persistence across runs.

### Workflow features
- **`strategy.matrix`** — fan a job across combinations of variables (e.g.
  multiple language versions).
- **`on` / triggers** — event-based triggering instead of "run the whole file
  now."
- **`runs-on`** — runner/label selection (currently rejected as an unknown key).
- **Per-step `timeout` / `continue-on-error`** — bound a step's runtime; allow a
  job to proceed past a failed step.
- **Configurable `shell:`** — v1 hardcodes `sh -c`; allow `bash` etc. per
  job/step.
- **Shared dependency cache mount** — a persistent, cross-job cache directory
  bind-mounted into each container (e.g. Go module cache, npm, pip) so jobs
  don't re-download dependencies every run. Today each job starts with an
  empty `/workspace` and no shared cache; `inherit:` hands files to a single
  child but is not a general cache. Needs a design pass: cache location
  per language/runtime, key/scope, and concurrent-write safety across
  parallel jobs.
- ~~**Built-in pipeline env vars**~~ — **shipped** (`internal/builtinenv`).
  Values latchet injects into every step
  automatically, before user-defined `env:` merges on top (so they can be
  overridden / faked for testing). All names are `LATCHET_*`-prefixed so
  they cannot collide with anything the workflow or container already
  defines. Shipped set:
  - `LATCHET_WORKSPACE` — the container-side workspace path (always
    `/workspace` in v2/v3; named for scripts that want to be
    path-agnostic).
  - `LATCHET_GIT_URL`, `LATCHET_GIT_BRANCH`, `LATCHET_GIT_TAG`,
    `LATCHET_GIT_SHA`, `LATCHET_GIT_REF` — populated from the
    `latchet watch` trigger when available; falls back to shelling out
    to `git` on the host CWD (`git remote get-url`, `git symbolic-ref`,
    `git describe --tags`, `git rev-parse HEAD`) when not. Empty string
    if neither source is available (e.g. running in a non-git directory
    standalone).
  - `LATCHET_RUN_ID` — latchet's run id (matches the workspace/log dir
    name).
  - `LATCHET_JOB_ID` — the current job's id.
  Note: distinct from existing `LATCHET_*` env vars the binary *reads*
  to configure itself (`LATCHET_RUNTIME`, `LATCHET_WORKSPACE_ROOT`,
  `LATCHET_KEEP_WORKSPACE`, `LATCHET_LOG_DIR`) — those names are
  reserved on input; the injected ones above are output-only.
  Resolved at implementation: the git facts come from a best-effort
  `git` probe of the host CWD (empty strings outside a checkout or with
  no `git`); `LATCHET_GIT_TAG` uses `describe --tags --exact-match` (tag
  only when HEAD *is* a tag); `LATCHET_GIT_REF` is derived from
  branch/tag with branch preferred (no extra `git` call); the vars are
  injected only inside containers, not exported on the host process.
  Still open for `latchet watch`: letting a trigger's known ref/SHA
  override the CWD probe.

### Operational
- **Minimal web UI** — a read-only local dashboard for browsing runs and
  their logs, served by a new `latchet ui` subcommand (`latchet ui --port
  8080`). Strictly an observability layer over what's already on disk; it
  does **not** trigger, schedule, or mutate runs. Scope:
  - **Single static binary, no external deps.** Serve from Go's
    `net/http`; embed assets with `embed.FS`. No Node build step, no
    database — the persistent per-run log directory (with its `latest`
    symlink) is the source of truth.
  - **Views.** A run list (most-recent first, parsed from the log dir
    names / `latest`), a per-run page showing each job's status and a
    link to its log, and raw per-job log streaming. Job status comes
    from the existing run summary; live tailing of an in-flight run is a
    follow-up, not v1.
  - **Localhost-only by default.** Bind `127.0.0.1`; no auth, no TLS.
    Exposing it on a network is the operator's call and out of scope for
    the minimal cut.
  - Depends on nothing new; reads `logstore`'s existing layout. Pairs
    naturally with the **provenance** work — once `provenance.json` lands
    per run, the run page can surface the attestation. Bigger questions
    (triggering runs from the UI, multi-host aggregation, websockets for
    live logs) are explicit non-goals for the minimal version.
- **Secret masking** — redact secret values from streamed logs.
- **Workspace retention sweeper** — auto-clean old run directories from temp.
- **CLI flags** — `validate-only`, `dry-run`, and a real argument parser (v1
  takes no args).
- **Release pipeline** — tagged releases with published prebuilt binaries.
  A CI workflow that, on a version tag, cross-compiles `latchet` for
  linux/macOS/Windows × amd64/arm64 and uploads the binaries (plus checksums)
  as release assets. **Prerequisite for the installation scripts below** —
  the installers have nothing to download until this exists.
- **Automated installation scripts** — one-line installers that fetch the
  right prebuilt binary and put it on `PATH`:
  - **Linux** — `install.sh` (curl-pipe friendly); detect arch (amd64/arm64).
  - **macOS** — `install.sh` covering Intel and Apple Silicon; consider a
    Homebrew formula/tap as a follow-up.
  - **Windows** — `install.ps1` for PowerShell.
  Depends on the release pipeline above for binaries to download.
- **Global `latchet-ci.yml` config** — a machine-wide config file (separate
  from the per-project workflow `latchet.yml`) for user defaults: preferred
  container runtime, workspace root, default env, log verbosity. Loaded from a
  standard location (`$XDG_CONFIG_HOME/latchet/latchet-ci.yml`, `~/.config/...`,
  or `%APPDATA%` on Windows) and overridden by per-project settings and
  environment variables.
- **`latchet watch` — git change monitoring** — a one-shot command that
  checks each watched repo configured in the global `latchet-ci.yml` for
  new commits on configured branches and tags; when any watched ref
  advances (or, for tags, appears or moves), latchet fetches the new
  commit and runs that repo's `latchet.yml`. Constraints:
  - **Branches and tags only.** No PR / merge-request triggers. Each
    entry in the global config is a git URL plus a list of branches
    and/or a list of tag patterns (e.g. exact tags like `v1.0.0`, or
    globs like `v*`).
  - **SSH only** for git access. The user's existing SSH key is used;
    no HTTPS / token handling.
  - **No internal timer.** `latchet watch` does one pass and exits;
    schedule it with system cron (or any other scheduler) for periodic
    checks. Keeps latchet stateless-as-a-process and avoids reinventing
    cron.
  - **State per (repo, ref)** — last-seen SHA persisted under
    `$XDG_STATE_HOME/latchet/watch/` so a change is detected exactly
    once. First run after a new repo or new tag-pattern is added is a
    no-op (records current SHAs without firing). New tags matching a
    watched pattern fire on first sight; tag moves fire (the SHA
    changed).
  - Depends on the global-config item above (where repo URLs, branch
    lists, and tag patterns live).

## Supply chain & attestation (standout)

The defining strategic bet: every workflow run produces a signed in-toto
[SLSA v1.0 provenance attestation](https://slsa.dev/spec/v1.0/provenance) by
default. Makes latchet the only minimal CI engine that ships SLSA L1+
out of the box, and lays the foundation for a verifier mode that
complements the existing executor plane. Pairs with the existing per-job
log files and release pipeline: every tagged release ships attested
binaries, and every local run emits a manifest a downstream consumer can
re-verify.

**Why latchet, why now.** Minimal CI tools (act, drone-cli, every project's
home-rolled Makefile) emit zero provenance. Big platforms emit it only
opt-in: GitHub Artifact Attestations needs an explicit workflow step;
Tekton Chains is its own subsystem. None ship by default in a small
binary. Sigstore made keyless signing tractable for non-experts (cosign,
Fulcio, Rekor are widely deployed in 2026), and AI-generated code
pipelines amplify supply-chain risk — per-run attestation is becoming
table-stakes for any code touched by agentic tooling.

The architectural framing (from the executor-vs-verifier conversation):
latchet is the executor; this feature is what lets latchet's outputs
plug into a verifier model without bolting on a separate trust plane.

### Subsystem 1 — Provenance emission (small; gets every run to SLSA L1)

> **Shipped** (`internal/provenance`, wired in `engine.Run`). Every executed
> run writes `<logdir>/provenance.json`: an in-toto statement with a SLSA
> v1.0 predicate (subjects hashed from job workspaces, images digest-pinned
> via `runtime.ImageDigest`, workflow SHA, git source, builder + timestamps).
> Best-effort; never changes exit code. Open follow-ups below remain:
> secret-value redaction (a no-op `provenance.Redact` seam exists, awaiting
> the secret-masking item) and an `artifacts:` selector to scope large
> workspaces. See [`docs/provenance-plan.md`](docs/provenance-plan.md).

After each run, write `<logdir>/provenance.json` per SLSA v1.0 schema
inside an [in-toto attestation](https://github.com/in-toto/attestation)
envelope (`statement.predicate = slsaprovenance/v1`). Contents:

- **subject[]** — SHA256 of each artifact found at end-of-job under
  `/workspace`, one entry per file (or aggregated per job — design
  call).
- **predicate.buildDefinition.buildType** — a latchet-specific URI like
  `https://latchet.dev/buildtypes/v1`.
- **predicate.buildDefinition.externalParameters** — workflow file SHA
  (of the on-disk `latchet.yml` at run time), invocation arguments
  (`file`, `max_parallel`, `dry_run`).
- **predicate.buildDefinition.internalParameters** — merged env (secret
  values redacted when secret-masking is enabled), per-step `run`
  strings, container image references *as written in YAML*.
- **predicate.buildDefinition.resolvedDependencies** — image references
  *as resolved at pull time* (digest-pinned, e.g.
  `docker.io/library/golang@sha256:…`), so the manifest pins exactly
  the byte-level image used.
- **predicate.runDetails.builder.id** — `https://latchet.dev/builders/v3+`
  plus `internal/version.Version`+`Commit`.
- **predicate.runDetails.metadata.invocationId** — latchet's run id
  (matches the workspace and log dir name).
- **predicate.runDetails.metadata.startedOn / finishedOn** — ISO 8601.

No reproducibility required for L1 — the provenance just needs to exist
and be faithful. Most CI workflows today are SLSA L0; landing this
takes any latchet run to L1 with no user action required.

Open design questions: per-job-attestation vs single-run-attestation
(multi-subject); secret-value redaction policy; how to record "subjects"
for effect-only jobs (deploy, notify) that produce no file outputs.

### Subsystem 2 — Determinism helpers (small; raises the ceiling of what's verifiable)

Optional knobs that remove the cheapest sources of nondeterminism so a
larger fraction of any workflow's output is reproducible (and therefore
verifiable). Activated by `deterministic: true` per-job or workflow-level,
or by `LATCHET_DETERMINISTIC=1`:

- Inject `SOURCE_DATE_EPOCH` (derived from `LATCHET_GIT_SHA`'s commit
  timestamp when available, else the run-start time).
- Set `LC_ALL=C`, `TZ=UTC`, `LANG=C` in step env.
- Pass `--mtime` / `--sort=name` hints to common archive tools via a
  small documented shim ("if you tar, use these flags").
- Document the `SOURCE_DATE_EPOCH`-aware toolchains (Go, recent npm,
  cargo with `-Zremap-debuginfo`, gcc with `-ffile-prefix-map`) as
  best-effort tips, not enforced guarantees.

This does **not** enforce reproducibility — that lives in the toolchain
and the workflow author's discipline. Subsystem 2 is 80/20 triage that
makes verifier-mode genuinely useful for ordinary workflows; hermetic
guarantees (Nix-grade) are out of scope and always will be.

### Subsystem 3 — Sigstore signing (small once `cosign` is on the host)

> **Shipped (key-based local path)** (`internal/signer`, wired in
> `engine.Run`). When `LATCHET_COSIGN_KEY` is set and `cosign` is on PATH,
> the run signs `provenance.json` with `cosign sign-blob`
> (`--tlog-upload=false` by default; `LATCHET_COSIGN_TLOG=1` opts into Rekor),
> writing `provenance.json.sig`. Soft dependency: missing cosign or no key
> leaves the attestation unsigned; best-effort, never changes the exit code.
> **Still open:** the keyless Fulcio/OIDC release path below (no key on disk),
> which lands with the **Release pipeline** item; and `cosign attest`/OCI
> signing once the release pipeline pushes images.

After provenance emission, optionally sign the attestation and publish
to a transparency log:

- For file artifacts: `cosign attest-blob --predicate provenance.json
  --type slsaprovenance1 <artifact>` per subject.
- For OCI artifacts (the release pipeline pushing container images,
  when that lands): `cosign attest --predicate ... <image-ref>`.
- For tagged releases (`.github/workflows/release.yml`): cosign uses
  GitHub Actions OIDC to mint a short-lived Fulcio cert — **no key
  material on disk** — and pushes the signature to the
  [Rekor](https://docs.sigstore.dev/logging/overview/) append-only
  transparency log.
- Downstream verification is one command:
  `cosign verify-blob-attestation --type slsaprovenance1
  --certificate-identity-regexp '^https://github\.com/thowd22/latchet/' …
  <artifact>` against the published cert identity.

`cosign` is a **soft dependency** — if absent from PATH, latchet emits
the unsigned attestation and logs a single line ("cosign not found,
attestation unsigned"). No hard install requirement.

### Subsystem 4 — `latchet verify <manifest>` (medium; the verifier role made real)

> **Shipped (core)** (`engine.Verify`, `latchet verify` subcommand). Loads a
> provenance.json, checks the on-disk workflow SHA matches the manifest,
> re-runs the workflow with images pinned to the recorded digests, re-hashes
> subjects, and compares — writing `<logdir>/verification.json`. Modes:
> `--lax` (default; passes when every subject is reproduced by name) and
> `--strict` (bit-for-bit). `--explain` prints expected-vs-actual hashes.
> **Still open:** byte-level `diffoscope` diffing (needs the original artifact
> bytes, which the manifest doesn't carry — only hashes); verifying a *signed*
> bundle's signature as part of verify; `source`-based checkout when the
> workflow itself doesn't clone.

The standout differentiator from any other minimal CI tool: any user
can re-derive any other user's claimed build, locally, in one command.

```sh
latchet verify provenance.json
latchet verify --strict provenance.json
latchet verify --explain provenance.json   # diffoscope output on mismatch
```

Operation:

1. Parse the SLSA statement; extract resolved image digests, workflow
   SHA, source SHA, step commands, merged env.
2. Re-run the workflow against the recorded inputs in a fresh
   workspace, pinning the same image digests.
3. Hash the re-derived subjects and compare.

Modes:

- `--strict` — every subject must match bit-for-bit; any mismatch is an
  error. Only useful when the workflow is fully reproducible.
- `--lax` (default) — match what we can; list mismatches as warnings;
  exit 0 if the *workflow structure* and the *deterministic subjects*
  match even when full outputs differ. Honest behavior for the common
  case where reproducibility is partial.
- `--explain` — for each mismatch, shell out to `diffoscope` (if
  available) and include the structural diff in a verification report
  at `<logdir>/verification.json`.

Useful for: release maintainers re-verifying contributor builds, CI
gates checking upstream attestations, adversarial verification by
independent parties (informal quorum trust).

### SLSA level mapping — what latchet can credibly claim

| Level | Requirement (SLSA v1.0) | latchet status |
|-------|--------------------------|----------------|
| L1    | Provenance exists, generated by the build platform | ✅ Subsystem 1 (every run) |
| L2    | Hosted build platform; signed provenance | ✅ Subsystem 1+3 for release-pipeline builds running in GHA; ❌ for local `latchet` runs (a laptop is not a "hosted build platform" by SLSA's definition) |
| L3    | Hardened, isolated builder; signing keys inaccessible to build steps | ❌ Out of reach without a separate trusted backplane; Fulcio's keyless flow gives us the *signing-key isolation* piece but not the *hardened builder* piece |
| L4    | Hermetic, reproducible | ❌ Requires hermetic toolchains; not in latchet's gift |

The honest framing: **latchet ships SLSA L1 by default, L2 for releases
built on GitHub Actions, and exposes verifier tooling that builds
informal trust above that through quorum / independent verification —
but it does not and cannot claim L3/L4 without a separate
trusted-execution backplane.** Marketing this honestly is itself a
differentiator; most CI vendors overclaim.

### Dependencies & sequencing

1. **Subsystem 1** (provenance emission) — fully independent; ship first.
2. **Subsystem 3** (sigstore signing) — depends on 1; trivial follow-on
   once cosign is wired.
3. **Subsystem 2** (determinism helpers) — independent; gains real value
   only alongside 4.
4. **Subsystem 4** (verify mode) — depends on 1 (+2 to be useful);
   medium effort; honest value depends on workflow-level
   reproducibility that's largely upstream.

Loose dependency on the **secret masking** roadmap item — provenance
must redact secret env values, so the masking implementation feeds the
provenance writer. Loose dependency on **`latchet watch`** — when watch
triggers a run, the `externalParameters` should record the trigger
(repo, ref, SHA) so the attestation reflects the cause-of-build.

### Out of scope (deferred or never)

- Hosted verifier service / SaaS — this is CLI tooling, not a server.
- Hermetic toolchains — Nix / Bazel handle that; latchet doesn't try.
- VEX / vulnerability assertions — orthogonal supply-chain concern;
  pair with `grype` / `trivy` externally.
- SBOM generation — would pair well (`syft`, `cyclonedx-cli`) but is a
  separate feature with its own design.
- Multi-party quorum verification / threshold signing — interesting
  but well beyond v3+; the verify command supports informal quorum
  (run it on N machines, compare reports) without protocol-level
  support.

### Honest limits — what to say out loud

1. **Reproducibility is mostly upstream.** Latchet can emit provenance
   and verify what's reproducible; it cannot make `cargo build`
   deterministic when it isn't. Subsystem 2 is triage, not a guarantee.
2. **L3+ requires a trusted backplane.** A laptop signing its own
   provenance does not satisfy SLSA L3, no matter how clean the
   attestation. Don't market L3+ from local runs.
3. **The verifier mode's value scales with adoption.** One person
   running `latchet verify` proves little; ten independent parties
   running it on the same manifest is the actual trust mechanism. The
   tool ships the capability; the trust comes from social use.

## Suggested ordering

Done so far:
1. ~~Parallel job execution~~ — shipped in v0.2.0.
2. ~~CLI flags~~ — shipped in v0.2.0.
3. ~~Workspace inheritance~~ — shipped in v0.3.0 (covers the
   parent-fan-out subset of cross-job artifacts).
4. ~~Built-in pipeline env vars~~ (`LATCHET_*`) — shipped
   (`internal/builtinenv`); injects WORKSPACE/RUN_ID/JOB_ID + GIT_*
   facts below user env.

Done so far (cont.):
5. ~~**Supply chain & attestation, Subsystem 1**~~ (provenance emission) —
   shipped (`internal/provenance`); every run emits a SLSA v1.0
   `provenance.json` → SLSA L1.
6. ~~**Supply chain & attestation, Subsystem 3**~~ (sigstore signing,
   key-based local path) — shipped (`internal/signer`); signs
   `provenance.json` via cosign when `LATCHET_COSIGN_KEY` is set. The
   keyless release-pipeline path remains open (see Subsystem 3 note).
7. ~~**Supply chain & attestation, Subsystem 4**~~ (verify, core) — shipped
   (`engine.Verify`, `latchet verify`); re-derives a run from its
   provenance and compares subjects (strict/lax). diffoscope/--explain
   byte-diffing and signed-bundle verification remain open.

Next picks (in rough order of value-per-effort):
1. **Global `latchet-ci.yml` + `latchet watch`** — turns latchet into a
   minimal CI server you can run from cron.
3. **`uses` / reusable actions** — still the largest single item; do
   it once the engine is stable and the supply-chain story is in
   place (so fetched actions can be verified).

Everything else can follow demand.
