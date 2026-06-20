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

#### Prebuilt actions / build steps

A catalog of pre-packaged, reusable steps a workflow can invoke — the concrete
payload of the `uses` item above. Each would be a versioned unit of common
build/publish behavior so authors don't hand-roll it inline. The list is just
getting started; seeds below (signed OCI builds first):

- **Signed OCI image build** *(prebuild action)* — build a container image
  from a Dockerfile + context, push it to a registry, and `cosign attest` the
  resulting image keyless (Fulcio/OIDC + Rekor), reusing the signing path
  already shipped for releases (`internal/signer`,
  `.github/workflows/release.yml`). This is what finally gives the
  `cosign attest`/OCI signing deferred under
  [Subsystem 3](#subsystem-3--sigstore-signing-small-once-cosign-is-on-the-host)
  an artifact to sign — latchet does not build or push images today. Open
  design: builder backend (`docker build` / `buildah` / BuildKit), registry
  auth, and whether the image digest + attestation fold into the run's
  `provenance.json` as a subject / resolvedDependency.
- **Checkout** *(prebuild action)* — a built-in repository checkout, removing
  the "every job clones its own source" boilerplate (latchet has no implicit
  checkout today; see [README](README.md#checking-out-your-code)). Clones
  `LATCHET_GIT_URL` at `LATCHET_GIT_SHA` into `/workspace`.
- **Dependency cache** *(prebuild action)* — restore/save a keyed cache
  (Go modules, npm, pip, …); the step form of the
  [shared cache mount](#workflow-features) item.
- **Discover open PRs / MRs** *(prebuild action)* — query the host platform
  for the repo's open pull/merge requests and expose them to the workflow
  (number, source branch, head SHA, base branch, title, author, labels), so a
  workflow can run checks against each. Design tenets, in latchet's idiom:
  - **CLI adapters, no SDK.** Shell out to `gh pr list --json …` /
    `glab mr list -F json` as **soft dependencies** (reusing the user's
    existing `gh`/`glab` auth), instead of vendoring a GitHub/GitLab Go SDK —
    preserves the one-dependency rule and stays provider-agnostic. Provider is
    detected from the remote host (`github.com` → `gh`, `gitlab.*` → `glab`),
    overridable; self-hosted instances supported via the CLIs' own config.
  - **Output as data, not control flow.** Writes the PR/MR list as JSON to
    `/workspace` (and/or step outputs, once those exist) for downstream steps
    to consume. latchet has no token handling — auth lives entirely in the
    CLI.
  - **Pairs with fan-out.** Acting *per* PR/MR needs
    [`strategy.matrix`](#workflow-features) or dynamic job generation (neither
    exists yet), so discovery ships first and the per-PR fan-out follows.
  - Complements `latchet watch` (Operational section), which is intentionally
    branches/tags only — this is the opt-in building block toward the deferred
    PR/MR-trigger story, without baking provider APIs into the core.
- **AI build steps** *(prebuild actions)* — LLM-backed steps for the assistive
  parts of a pipeline: review a diff, summarize a PR/MR (pairs with **Discover
  open PRs / MRs** above), draft release notes / changelogs, triage test
  failures, or generate docs. Shared design: input read from `/workspace` (the
  diff, files), output written back to `/workspace`; the API key is provided
  via a `secrets:` entry (now shipped), so it's injected into the step and
  masked in logs and `provenance.json` rather than leaking. Unlike the
  CLI-adapter action above, a
  prebuilt action runs as its **own container image**, so it may bundle a
  provider SDK internally without touching latchet's one-dependency rule. Three
  flavors:
  - **OpenAI-compatible** *(provider-agnostic)* — calls any
    `/v1/chat/completions`-shaped endpoint via a configurable base URL + model
    + API key, so one action serves OpenAI, Azure OpenAI, OpenRouter, Together,
    and local/self-hosted servers (Ollama, vLLM, llama.cpp). The portable
    default.
  - **Claude (Anthropic)** *(provider-specific)* — uses the native Anthropic
    **Messages API** (`ANTHROPIC_API_KEY`, official `anthropic-sdk-go`) so it
    can reach Claude-specific capabilities the compatible shape can't express:
    adaptive thinking + `effort`, the 1M-token context window, prompt caching,
    vision / PDF input, structured outputs, and tool use. Defaults to the
    latest, most capable model (`claude-opus-4-8`; also `claude-sonnet-4-6` /
    `claude-haiku-4-5` / `claude-fable-5`), and can target the first-party API,
    Amazon Bedrock, Google Vertex AI, or Microsoft Foundry.
  - **ChatGPT (OpenAI)** *(provider-specific)* — uses OpenAI's native API
    (Responses / Chat Completions, `OPENAI_API_KEY`) for OpenAI-specific
    features beyond the portable `/v1/chat/completions` subset (native
    structured outputs, function calling, the Responses API tool surface).
- _(more to come — SBOM generation (`syft`), artifact upload/download, etc.)_

- ~~**Parallel job execution**~~ — **shipped** (v0.2.0). Jobs whose `needs` are
  all satisfied run concurrently (cap with `-max-parallel`); per-job log files
  keep concurrent output from interleaving.
- **Named artifacts (`upload-artifact` / `download-artifact`-style)** — pass
  selected files between arbitrary jobs by name. v3 added single-parent
  workspace inheritance (`inherit: <jobid>`) covering the common
  parent-to-children case; this item covers the harder cases: many-to-many,
  fan-in merges, exclude patterns, and persistence across runs.

### Workflow features
- **Run location (`LATCHET_LOCATION`)** — let a run know whether it's executing
  on the latchet server vs a developer workstation, so steps/jobs can be skipped
  or gated by environment (e.g. only deploy from the server, skip slow
  integration tests on a laptop).
  - **Where the value lives — machine-scoped, not per-project.** A per-project
    `latchet.yml` is byte-identical on every machine, so a `location:` *there*
    can't differentiate them. The location belongs in the **machine-scoped
    global config** (`latchet-ci.yml`,
    `location: server | local | <any string>`), with a `LATCHET_LOCATION` env
    var override (highest precedence) and a default of `local`. The latchet
    server's global config sets `location: server`; a workstation leaves it
    unset → `local`. `latchet watch` runs (which execute on the server) pick up
    the server's value automatically.
  - **Inject `LATCHET_LOCATION`** as a built-in step var (alongside the existing
    `LATCHET_*`), overridable like the others — so `run:` scripts can branch on
    it today: `if [ "$LATCHET_LOCATION" = server ]; then ./deploy; fi`. This
    half is small (a `globalconfig` field + one `builtinenv` var) and could ship
    on its own.
  - **Conditional execution (follow-on, larger).** A `when:`/`if:` on jobs and
    steps — e.g. `when: $LATCHET_LOCATION == server` — to *skip* a job/step
    rather than gate inside a `run:`. latchet has no conditional execution
    today; this is the bigger piece and would generalize beyond location (any
    expression), pairing with `strategy.matrix` and `on:` gating. The DAG
    skip-propagation machinery already exists, so a skipped-by-condition job
    behaves like one whose dependency was skipped.
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
- ~~**Secret masking**~~ — **shipped** (`internal/mask`, `secrets:` schema).
  A `secrets:` list of host env var names (workflow/job level) injects those
  values into a job's steps and masks them everywhere latchet writes:
  - **Mask in logs** — a streaming redactor (`internal/mask`) replaces secret
    values with `***` in step output and per-job log files, holding back a
    tail so a secret split across output chunks is still caught.
  - **Redact in provenance** — `provenance.Redact`/`RedactString` (no longer a
    no-op) drop secret values from `internalParameters` and per-step run
    strings, closing the former plaintext-env caveat in `provenance.json`.
  - **Still open:** secrets sourced from a file / external store (only host env
    today); a secret whose value is a short common substring over-masks (noted
    in the README).
- **Workspace retention sweeper** — auto-clean old run directories from temp.
- ~~**CLI flags**~~ — **shipped** (v0.2.0). `-file`, `-validate-only`,
  `-dry-run`, `-max-parallel`, `-version`, `-help`/`-h`, and a real argument
  parser (`cmd/latchet/main.go`).
- ~~**Release pipeline**~~ — **shipped** (`.github/workflows/release.yml`). On
  a version tag it cross-compiles `latchet` for linux/macOS/Windows ×
  amd64/arm64, generates `SHA256SUMS`, **keyless-signs** the checksums with
  cosign via GitHub OIDC (Fulcio cert + Rekor bundle, no key on disk), and
  publishes everything as release assets — so releases are SLSA L2 and the
  installation scripts have something to download. `workflow_dispatch` runs
  build+sign without cutting a release, for validation.
- ~~**Automated installation scripts**~~ — **shipped** (`scripts/install.sh`,
  `scripts/install.ps1`). One-line installers that fetch the right prebuilt
  binary (arch-detecting) and put it on `PATH`; `LATCHET_VERSION` /
  `LATCHET_INSTALL_DIR` honored. Exercised once a release is published (the
  release pipeline above provides the assets). Homebrew tap remains a
  follow-up.
- ~~**Global `latchet-ci.yml` config**~~ — **shipped** (`internal/globalconfig`).
  Machine-wide defaults (runtime, workspace root, log dir, `max_parallel`,
  default `env`, and the `watch:` repo list) loaded from `$LATCHET_CONFIG`,
  `$XDG_CONFIG_HOME/latchet/`, or `~/.config/latchet/`. Precedence: flags > env
  vars > global config > defaults; default `env` merges below a workflow's own.
  Strict parsing (unknown keys rejected). `%APPDATA%` on Windows is deferred.
  See [`docs/watch-plan.md`](docs/watch-plan.md).
- ~~**`latchet watch` — git change monitoring**~~ — **shipped**
  (`internal/watch`, `latchet watch` subcommand). One pass over the
  `watch:` repos in the global config: `git ls-remote` each, fire a run for
  any branch that advanced or tag (matching a `v*`-style glob) that appeared
  or moved, by cloning the commit and running its `latchet.yml`. State per
  `(repo, ref)` in `$XDG_STATE_HOME/latchet/watch/state.json` (override
  `LATCHET_WATCH_STATE`); first pass for a repo/tag-pattern baselines without
  firing, so each change fires exactly once. No internal timer — schedule with
  cron (validated end-to-end on the VM via cron + a local bare repo). The
  fire-decision logic is a pure, unit-tested function. A fired run gets the
  trigger's ref via `engine.Options.GitRef` → `builtinenv.OverrideRef`, so
  `LATCHET_GIT_BRANCH`/`REF` reflect the fired branch/tag even though the
  checkout is a detached SHA. Original design notes:
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
> Best-effort; never changes exit code. Secret-value redaction is now wired
> (`provenance.Redact`, via the shipped **secret masking** item). Open
> follow-up: an `artifacts:` selector to scope large workspaces. See
> [`docs/provenance-plan.md`](docs/provenance-plan.md).

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

> **Shipped** (`internal/builtinenv.Deterministic`, wired in `engine`).
> `deterministic: true` (workflow or job) / `LATCHET_DETERMINISTIC=1` injects
> `SOURCE_DATE_EPOCH` (HEAD commit time, else run-start fallback), `LC_ALL=C`,
> `LANG=C`, `TZ=UTC` at built-in (overridable) precedence. The archive-flag
> and toolchain guidance below ship as README documentation, not enforcement.

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
> **Keyless Fulcio/OIDC signing — shipped** in the release pipeline
> (`.github/workflows/release.yml`): on a tag, GitHub's OIDC mints a Fulcio
> cert (no key on disk) and `cosign sign-blob` signs `SHA256SUMS` into a Rekor
> bundle shipped as a release asset → SLSA L2 for releases. **Still open:**
> `cosign attest`/OCI signing — tracked as the **Signed OCI image build**
> entry under [Prebuilt actions / build steps](#prebuilt-actions--build-steps),
> since latchet needs to build/push an image before there's anything to attest.

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
> `--key <pub>` verifies the manifest's cosign signature bundle before
> re-running (fails fast on a tampered/unsigned manifest). **Still open:**
> byte-level `diffoscope` diffing (needs the original artifact bytes, which
> the manifest doesn't carry — only hashes); `source`-based checkout when the
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
7. ~~**Supply chain & attestation, Subsystem 4**~~ (verify) — shipped
   (`engine.Verify`, `latchet verify`); re-derives a run from its
   provenance and compares subjects (strict/lax), and `--key` verifies the
   manifest's signature bundle. diffoscope byte-diffing remains open.
8. ~~**Supply chain & attestation, Subsystem 2**~~ (determinism helpers) —
   shipped; `deterministic:` / `LATCHET_DETERMINISTIC=1` inject
   `SOURCE_DATE_EPOCH` + `LC_ALL`/`LANG`/`TZ`.

9. ~~**Release pipeline + keyless signing**~~ — shipped
   (`.github/workflows/release.yml`); tagged cross-compiled releases with
   cosign keyless-signed `SHA256SUMS` (Fulcio + Rekor) → SLSA L2 for releases.
10. ~~**Automated installation scripts**~~ — shipped (`scripts/install.sh`,
    `scripts/install.ps1`).
11. ~~**Global `latchet-ci.yml` config**~~ — shipped (`internal/globalconfig`);
    machine-wide defaults + the `watch:` repo list, with flags > env > config
    precedence. Unblocks `latchet watch`.
12. ~~**Secret masking**~~ — shipped (`internal/mask`, `secrets:` schema);
    host-env secrets injected into steps and masked in logs + `provenance.json`.
13. ~~**`latchet watch`**~~ — shipped (`internal/watch`); cron-scheduled git
    change monitoring that runs a repo's latchet.yml on new commits/tags.
    latchet is now a minimal CI server. Validated on the VM via cron.

The supply-chain arc (Subsystems 1–4 + keyless release signing) is now
complete. The only remaining pieces are genuinely out of scope or dependent on
features that don't exist yet: `cosign attest`/OCI signing needs the engine to
build/push container images (it doesn't) — now tracked as the **Signed OCI
image build** prebuilt action; `diffoscope` byte-diffing in `verify
--explain` needs the original artifact bytes, which the manifest deliberately
records only as hashes.

Next picks (in rough order of value-per-effort):
1. **`uses` / reusable actions** (and the **Prebuilt actions / build steps**
   catalog, incl. signed OCI builds — credential-taking actions are now
   unblocked by secret masking) — still the largest single item; do it once
   the engine is stable and the supply-chain story is in place (so fetched
   actions can be verified).

Everything else can follow demand.
