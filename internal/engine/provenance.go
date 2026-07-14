package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/thowd22/latchet/internal/builtinenv"
	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/logstore"
	"github.com/thowd22/latchet/internal/provenance"
	"github.com/thowd22/latchet/internal/signer"
	"github.com/thowd22/latchet/internal/version"
	"github.com/thowd22/latchet/internal/workspace"
)

// emitProvenance writes <logdir>/provenance.json for a completed run. It is
// best-effort: any failure is reported as a warning and never changes the
// run's exit code. Must be called after the run completes but before the
// workspace is cleaned, so job artifacts are still on disk to hash.
func emitProvenance(ctx context.Context, ws *workspace.Run, ls *logstore.Run, wf *config.Workflow, opts Options, git builtinenv.Git, images *imageCache, jobOuts *jobOutputs, maxParallel int, started, finished time.Time, out, warn io.Writer) {
	wfBytes, err := os.ReadFile(opts.File)
	if err != nil {
		fmt.Fprintf(warn, "latchet: provenance skipped: %v\n", err)
		return
	}

	ids := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var (
		subjects   []provenance.Subject
		totalFiles int
		totalBytes int64
		jobs       []provenance.JobParams
	)
	for _, id := range ids {
		job := wf.Jobs[id]

		if dir, derr := ws.JobDir(id); derr == nil {
			subs, stats, herr := provenance.HashTree(dir, id)
			if herr != nil {
				fmt.Fprintf(warn, "latchet: provenance: hashing %s: %v\n", id, herr)
			} else {
				subjects = append(subjects, subs...)
				totalFiles += stats.Files
				totalBytes += stats.Bytes
			}
		}

		builtins := jobBuiltins(ws.ID, job, wf, git)
		secretEnv := resolveSecrets(wf, job)
		secrets := secretValues(secretEnv)
		needsEnv := jobOuts.needsEnv([]string(job.Needs))
		// Record the actually-executed steps (call: and uses: steps inlined),
		// mirroring the engine's expansion so the attestation reflects what ran.
		staticBase := mergeEnv(builtins, needsEnv, wf.Env, job.Env, secretEnv)
		jobSteps := config.ExpandCalls(job.Steps, wf.Functions, wf.Keys, func(v string) string {
			return config.ExpandVars(v, staticBase)
		})
		steps := make([]provenance.StepParams, 0, len(jobSteps))
		for _, st := range jobSteps {
			// Mirror the runtime env merge (incl. needs outputs + secrets), then
			// redact any value (or run string) carrying a secret before recording.
			merged := mergeEnv(builtins, needsEnv, wf.Env, job.Env, secretEnv, st.Env)
			steps = append(steps, provenance.StepParams{
				Name: st.Name,
				Run:  provenance.RedactString(st.Run, secrets),
				Env:  provenance.Redact(merged, secrets),
				If:   st.If,
				Elif: st.Elif,
				Else: st.Else,
			})
		}
		jobs = append(jobs, provenance.JobParams{ID: id, Image: job.Container, Steps: steps})
	}

	var source *provenance.SourceRef
	if git.URL != "" || git.SHA != "" || git.Ref != "" {
		source = &provenance.SourceRef{URI: git.URL, Ref: git.Ref, Revision: git.SHA}
	}

	st := provenance.Build(provenance.Input{
		RunID:          ws.ID,
		Started:        started,
		Finished:       finished,
		BuilderVersion: version.Version,
		BuilderCommit:  version.Commit,
		WorkflowPath:   opts.File,
		WorkflowSHA:    provenance.SHA256Hex(wfBytes),
		Invocation: map[string]string{
			"file":         opts.File,
			"max_parallel": strconv.Itoa(maxParallel),
			"dry_run":      "false",
		},
		Source:   source,
		Jobs:     jobs,
		Images:   images.ResolvedDigests(),
		Subjects: subjects,
	})

	path, err := provenance.Write(ls.Dir, st)
	if err != nil {
		fmt.Fprintf(warn, "latchet: provenance skipped: %v\n", err)
		return
	}
	if totalFiles > 0 {
		fmt.Fprintf(out, "latchet: provenance at %s (%d artifact(s), %d bytes hashed)\n", path, totalFiles, totalBytes)
	} else {
		fmt.Fprintf(out, "latchet: provenance at %s\n", path)
	}

	signProvenance(ctx, path, out, warn)
}

// signProvenance signs the provenance file with cosign when a signing key is
// configured via LATCHET_COSIGN_KEY. Best-effort: a missing cosign or a
// signing failure is reported as a warning and never changes the exit code.
// LATCHET_COSIGN_TLOG=1 opts into uploading the signature to a Rekor
// transparency log (off by default, so signing works offline).
func signProvenance(ctx context.Context, provPath string, out, warn io.Writer) {
	keyPath := os.Getenv("LATCHET_COSIGN_KEY")
	if keyPath == "" {
		return // signing not requested
	}
	if !signer.Available() {
		fmt.Fprintf(warn, "latchet: cosign not found; attestation unsigned\n")
		return
	}
	tlog := os.Getenv("LATCHET_COSIGN_TLOG") == "1"
	sigPath, err := signer.SignBlob(ctx, keyPath, provPath, tlog)
	if err != nil {
		fmt.Fprintf(warn, "latchet: signing provenance: %v\n", err)
		return
	}
	fmt.Fprintf(out, "latchet: provenance signed -> %s\n", sigPath)
}

// mergeEnv overlays env levels low-to-high precedence into a single map, the
// map form of envutil.Merge for recording in provenance.
func mergeEnv(levels ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range levels {
		for k, v := range l {
			out[k] = v
		}
	}
	return out
}
