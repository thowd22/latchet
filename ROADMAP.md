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
- **Cross-job artifacts / shared workspace** — pass files between jobs. Today
  each job's `/workspace` is isolated.

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

### Operational
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

## Suggested ordering

1. **Parallel job execution** — cheap, the groundwork exists, immediate speedup.
2. **CLI flags** (`validate-only`, `dry-run`) — small, improves the dev loop.
3. **`uses` / reusable actions** — the big one; do it once the engine is stable.

Everything else can follow demand.
