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

## Suggested ordering

1. **Parallel job execution** — cheap, the groundwork exists, immediate speedup.
2. **CLI flags** (`validate-only`, `dry-run`) — small, improves the dev loop.
3. **`uses` / reusable actions** — the big one; do it once the engine is stable.

Everything else can follow demand.
