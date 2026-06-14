# Plan — Supply chain & attestation, Subsystem 1 (provenance emission)

Implementation plan for the first slice of the
[supply-chain roadmap](../ROADMAP.md#supply-chain--attestation-standout).
Subsystem 1 is fully independent and ships first.

## Goal

After every executed run, write `<logdir>/provenance.json` — an
[in-toto attestation](https://github.com/in-toto/attestation) wrapping a
[SLSA v1.0 provenance](https://slsa.dev/spec/v1.0/provenance) predicate — so
every latchet run reaches **SLSA L1** with no user action. No new third-party
dependency: the statement structs are hand-rolled and marshaled with
`encoding/json`, preserving the repo's one-dependency rule.

## Design decisions (resolving the roadmap's open questions)

- **One run-level statement, multi-subject.** A single `provenance.json` per
  run; `subject[]` aggregates artifacts across all jobs. Matches
  `invocationId = run id`. Not per-job attestations.
- **Subjects = regular files under each job's final `/workspace`**, named
  `<jobID>/<relpath>`, SHA256 digest. Symlinks and special files are skipped.
  If a run produces **zero** file artifacts (effect-only deploy/notify runs),
  fall back to a single subject = the workflow file itself, so the statement
  is always in-toto-valid.
- **Emit on any completed run** — success *or* job-failure — as a faithful
  record. Not on infra-abort (exit 3), where nothing meaningfully executed.
  Emission failure logs a warning and never changes the run's exit code.
- **Redaction hook, no-op today.** `internalParameters` records merged env;
  values pass through a `redact()` seam that is identity until the
  secret-masking roadmap item lands. ⚠️ Until then, env *values* appear in
  plaintext in the local `provenance.json` (documented in the README).
- **Artifact-hashing cost** is the main risk: hashing a large `/workspace`
  (e.g. `node_modules`) is slow. v1 hashes everything but logs a one-line
  notice with file count / total bytes (no silent cap). A future `artifacts:`
  glob (the Named-artifacts roadmap item) will scope it.

## Changes, file by file

1. **`internal/runtime/runtime.go`** — add
   `ImageDigest(ctx, image) (string, error)` via
   `image inspect --format '{{index .RepoDigests 0}}'` (parallels the existing
   `inspectArgs`). Pure-argv builder + table test.
2. **`internal/engine/imagecache.go`** — record the resolved digest per image
   during `Ensure` (already the dedup chokepoint, already mutex-guarded);
   expose `ResolvedDigests() map[string]string` for `resolvedDependencies`.
3. **`internal/provenance/` (new package)**
   - `Statement` / `Subject` / `Predicate` structs → indented JSON.
   - `Build(Input) Statement` — deterministic (sorted subjects, sorted env),
     maps Input → SLSA v1.0 fields (`buildType =
     https://latchet.dev/buildtypes/v1`, `builder.id` from
     `version.Version`+`Commit`, ISO-8601 `startedOn`/`finishedOn`).
   - `HashTree(dir) ([]Subject, Stats)` — walk + SHA256 of regular files.
   - `Write(dir, Statement) (path, error)`.
   - Hermetic unit tests on a fixed `Input` and a temp dir (no runtime).
4. **`internal/engine/engine.go`** — capture `startedOn` before
   `scheduler.Run`, `finishedOn` after; on non-infra completion, gather
   subjects from each `ws.JobDir(id)` **before `ws.Cleanup`**, hash
   `opts.File` for the workflow SHA, assemble `provenance.Input` (reusing
   `git`, `wf`, `opts`, `images.ResolvedDigests()`), and
   `provenance.Write(ls.Dir, …)`. One log line: `latchet: provenance at <path>`.
5. **`README.md`** — short "Provenance" section (what's emitted, the SLSA L1
   claim, the plaintext-env caveat). Mark Subsystem 1 shipped in `ROADMAP.md`.
6. **`ci/runtests.sh`** — assert `provenance.json` exists; `predicateType` is
   `slsaprovenance/v1`; at least one image digest is `@sha256:`-pinned;
   subject count > 0; `invocationId` == run id.

## Sequencing

Two commits, both landed on `main`:

1. `runtime.ImageDigest` + imagecache digest capture (small, independently
   testable).
2. The `provenance` package + engine wiring + docs + harness checks.

Verified end-to-end on the VM with the dogfood workflow.

**Subsystem 3 (cosign signing)** is a tiny follow-on: sign `provenance.json`
when `cosign` is on PATH, else log "unsigned". No further design needed.

## SLSA field mapping (engine inputs already on hand)

| SLSA field | Source |
|---|---|
| `runDetails.metadata.invocationId` | `ws.ID` |
| `runDetails.metadata.startedOn` / `finishedOn` | `time.Now()` around `scheduler.Run` |
| `buildDefinition.externalParameters` (workflow SHA, args) | hash `opts.File`; `opts` |
| `buildDefinition.internalParameters` (env, run strings, images-as-written) | `wf` + `envutil.Merge` |
| `buildDefinition.resolvedDependencies` (digest-pinned images) | `imageCache.ResolvedDigests()` |
| `runDetails.builder.id` | `version.Version` + `version.Commit` |
| source ref/SHA | `builtinenv.ResolveGit` facts (already resolved per run) |
| `subject[]` | `provenance.HashTree` over job workspaces |
