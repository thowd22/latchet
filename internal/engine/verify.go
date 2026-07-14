package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thowd22/latchet/internal/builtinenv"
	"github.com/thowd22/latchet/internal/config"
	"github.com/thowd22/latchet/internal/dag"
	"github.com/thowd22/latchet/internal/keys"
	"github.com/thowd22/latchet/internal/log"
	"github.com/thowd22/latchet/internal/logstore"
	"github.com/thowd22/latchet/internal/provenance"
	"github.com/thowd22/latchet/internal/runtime"
	"github.com/thowd22/latchet/internal/scheduler"
	"github.com/thowd22/latchet/internal/signer"
	"github.com/thowd22/latchet/internal/workspace"
)

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// VerifyOptions configures a `latchet verify` invocation.
type VerifyOptions struct {
	ManifestPath string // path to provenance.json
	File         string // workflow override; default is the path recorded in the manifest
	Key          string // cosign public key; when set, verify <manifest>.bundle first
	Strict       bool   // require every subject to match bit-for-bit
	Explain      bool   // print per-subject mismatch detail
	MaxParallel  int
	DefaultEnv   map[string]string           // global-config default env, applied to the re-run
	Functions    map[string]*config.Function // global functions available to the re-run
	Stdout       io.Writer
	Stderr       io.Writer
}

// VerificationReport is written to <logdir>/verification.json.
type VerificationReport struct {
	Manifest  string            `json:"manifest"`
	Mode      string            `json:"mode"` // strict | lax
	Result    string            `json:"result"`
	Signature signatureCheck    `json:"signature"`
	Workflow  workflowCheck     `json:"workflow"`
	Subjects  subjectComparison `json:"subjects"`
}

type workflowCheck struct {
	Path        string `json:"path"`
	ExpectedSHA string `json:"expectedSha256"`
	ActualSHA   string `json:"actualSha256"`
	Match       bool   `json:"match"`
}

type signatureCheck struct {
	Checked bool   `json:"checked"`
	Valid   bool   `json:"valid"`
	Bundle  string `json:"bundle,omitempty"`
}

type subjectComparison struct {
	Matched    []string         `json:"matched"`
	Mismatched []mismatchDetail `json:"mismatched"`
	Missing    []string         `json:"missing"` // in manifest, not reproduced
	Extra      []string         `json:"extra"`   // reproduced, not in manifest
}

type mismatchDetail struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// Verify re-derives the build described by a provenance manifest and compares
// the result to the manifest's recorded subjects. It re-runs the recorded
// workflow in a fresh workspace with each image pinned to the digest recorded
// in resolvedDependencies, re-hashes the resulting artifacts, and reports.
//
// Exit codes: 0 verified, 1 verification failed (mismatch / workflow differs /
// a job failed), 2 bad manifest or workflow, 3 runtime/infra error.
func Verify(vo VerifyOptions) int {
	if vo.Stdout == nil {
		vo.Stdout = os.Stdout
	}
	if vo.Stderr == nil {
		vo.Stderr = os.Stderr
	}
	out, warn := vo.Stdout, vo.Stderr

	st, err := provenance.Load(vo.ManifestPath)
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitConfig
	}

	// Verify the manifest's own signature first, before trusting any value it
	// records. Fails fast on a tampered/unsigned manifest, before the re-run.
	sig := signatureCheck{}
	if vo.Key != "" {
		bundlePath := vo.ManifestPath + ".bundle"
		sig.Bundle = bundlePath
		switch {
		case !signer.Available():
			fmt.Fprintf(warn, "latchet verify: cosign not found; skipping signature check\n")
		case !fileExists(bundlePath):
			fmt.Fprintf(warn, "latchet verify: no signature bundle at %s; skipping signature check\n", bundlePath)
		default:
			sig.Checked = true
			if err := signer.VerifyBlob(context.Background(), vo.Key, bundlePath, vo.ManifestPath); err != nil {
				fmt.Fprintf(out, "latchet verify: FAILED — manifest signature did not verify\n  %v\n", err)
				return ExitFailed
			}
			sig.Valid = true
			fmt.Fprintf(out, "latchet verify: manifest signature OK (%s)\n", bundlePath)
		}
	}

	wfPath := st.WorkflowPath()
	if vo.File != "" {
		wfPath = vo.File
	}
	if wfPath == "" {
		fmt.Fprintf(warn, "latchet verify: manifest records no workflow path; pass --file\n")
		return ExitConfig
	}

	wfBytes, err := os.ReadFile(wfPath)
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitConfig
	}
	expectedSHA := st.WorkflowDigest()
	actualSHA := provenance.SHA256Hex(wfBytes)

	// Recipe identity: a build cannot be reproduced from a different workflow.
	if expectedSHA == "" || actualSHA != expectedSHA {
		fmt.Fprintf(out, "latchet verify: FAILED — workflow file does not match the manifest\n")
		fmt.Fprintf(out, "  workflow:        %s\n  expected sha256: %s\n  actual sha256:   %s\n",
			wfPath, dashIfEmpty(expectedSHA), actualSHA)
		return ExitFailed
	}

	wf, err := config.Load(wfPath)
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitConfig
	}
	wf.Functions = config.MergeFunctions(vo.Functions, wf.Functions)

	// Pin each uses: step to the key SHA recorded at the original run
	// (mirroring the image pinning below), so the re-run fetches the exact
	// key bytes even when a tag has since moved. The rewritten
	// url[//subpath]@sha string is itself a valid reference.
	pinnedKeys := st.ResolvedKeys()
	for _, id := range sortedJobIDs(wf) {
		for _, step := range wf.Jobs[id].Steps {
			if step == nil || step.Uses == "" {
				continue
			}
			if uri, ok := pinnedKeys[step.Uses]; ok && strings.HasPrefix(uri, "git+") {
				step.Uses = strings.TrimPrefix(uri, "git+")
			} else {
				fmt.Fprintf(warn, "latchet verify: no recorded pin for key %q (job %q); using as-is\n", step.Uses, id)
			}
		}
	}
	fns, _, err := keys.ResolveAll(context.Background(), wf, out)
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return exitFor(err)
	}
	wf.Keys = fns

	if err := wf.Validate(); err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitConfig
	}
	wf.Env = overlayDefaultEnv(vo.DefaultEnv, wf.Env)
	wf = config.ExpandMatrix(wf)

	// Pin each job's image to the digest recorded at the original run.
	pinned := st.ResolvedImages()
	ids := sortedJobIDs(wf)
	for _, id := range ids {
		job := wf.Jobs[id]
		if uri, ok := pinned[job.Container]; ok && uri != "" {
			job.Container = uri
		} else {
			fmt.Fprintf(warn, "latchet verify: no recorded digest for image %q (job %q); using as-is\n", job.Container, id)
		}
	}

	r, err := runtime.Detect()
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitInfra
	}
	g, err := dag.Build(wf.Deps())
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitConfig
	}
	ws, err := workspace.New()
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitInfra
	}
	ls, err := logstore.New(ws.ID)
	if err != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", err)
		return ExitInfra
	}

	fmt.Fprintf(out, "latchet verify: re-running %s (run %s) to re-derive subjects\n", wfPath, ws.ID)

	images := newImageCache()
	maxParallel := vo.MaxParallel
	if maxParallel < 1 {
		maxParallel = 1
	}
	// Re-run with the cause-of-build git facts recorded in the manifest, so
	// steps (and keys, e.g. checkout) that read LATCHET_GIT_URL/SHA reproduce
	// the original inputs rather than probing this host's CWD. CommitEpoch
	// falls back to now for SOURCE_DATE_EPOCH in deterministic workflows.
	git := builtinenv.Git{CommitEpoch: strconv.FormatInt(time.Now().Unix(), 10)}
	if src := st.Predicate.BuildDefinition.ExternalParameters.Source; src != nil {
		git.URL, git.SHA = src.URI, src.Revision
		git = builtinenv.OverrideRef(git, src.Ref)
	}

	jobOuts := newJobOutputs()
	results, infraErr := scheduler.Run(context.Background(), g, scheduler.Options{
		MaxParallel: maxParallel,
		RunJob: func(ctx context.Context, jobID string) (scheduler.Result, error) {
			return runOne(ctx, r, ws, ls, wf, jobID, images, out, maxParallel, git, jobOuts)
		},
		OnSkip: func(jobID, reason string) { log.JobSkip(out, jobID, reason) },
	})
	if infraErr != nil {
		fmt.Fprintf(warn, "latchet verify: %v\n", infraErr)
		if kept := ws.Cleanup(true); kept != "" {
			fmt.Fprintf(warn, "latchet verify: workspace kept at %s\n", kept)
		}
		return ExitInfra
	}

	runFailed := false
	for _, res := range results {
		if res.Status == scheduler.StatusFailed {
			runFailed = true
			break
		}
	}

	got := collectSubjects(ws, ids, warn)
	cmp := compareSubjects(st.SubjectDigests(), got)

	verified := !runFailed && len(cmp.Missing) == 0
	if vo.Strict {
		verified = verified && len(cmp.Mismatched) == 0 && len(cmp.Extra) == 0
	}

	report := VerificationReport{
		Manifest:  vo.ManifestPath,
		Mode:      modeName(vo.Strict),
		Result:    resultName(verified),
		Signature: sig,
		Workflow:  workflowCheck{Path: wfPath, ExpectedSHA: expectedSHA, ActualSHA: actualSHA, Match: true},
		Subjects:  cmp,
	}
	reportPath, werr := writeReport(ls.Dir, report)
	if werr != nil {
		fmt.Fprintf(warn, "latchet verify: writing report: %v\n", werr)
	}

	printVerifySummary(out, vo, report, reportPath, runFailed)

	ws.Cleanup(!verified) // keep the workspace on failure for inspection
	if verified {
		return ExitSuccess
	}
	return ExitFailed
}

func collectSubjects(ws *workspace.Run, ids []string, warn io.Writer) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		dir, err := ws.JobDir(id)
		if err != nil {
			continue
		}
		subs, _, herr := provenance.HashTree(dir, id)
		if herr != nil {
			fmt.Fprintf(warn, "latchet verify: hashing %s: %v\n", id, herr)
			continue
		}
		for _, s := range subs {
			out[s.Name] = s.Digest["sha256"]
		}
	}
	return out
}

func compareSubjects(want, got map[string]string) subjectComparison {
	var c subjectComparison
	for name, wsha := range want {
		gsha, ok := got[name]
		switch {
		case !ok:
			c.Missing = append(c.Missing, name)
		case gsha == wsha:
			c.Matched = append(c.Matched, name)
		default:
			c.Mismatched = append(c.Mismatched, mismatchDetail{Name: name, Expected: wsha, Actual: gsha})
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			c.Extra = append(c.Extra, name)
		}
	}
	sort.Strings(c.Matched)
	sort.Strings(c.Missing)
	sort.Strings(c.Extra)
	sort.Slice(c.Mismatched, func(i, j int) bool { return c.Mismatched[i].Name < c.Mismatched[j].Name })
	return c
}

func sortedJobIDs(wf *config.Workflow) []string {
	ids := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func writeReport(dir string, r VerificationReport) (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	p := filepath.Join(dir, "verification.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func printVerifySummary(out io.Writer, vo VerifyOptions, r VerificationReport, reportPath string, runFailed bool) {
	c := r.Subjects
	fmt.Fprintf(out, "\nlatchet verify (%s): %s\n", r.Mode, strings.ToUpper(r.Result))
	if r.Signature.Checked {
		fmt.Fprintf(out, "  signature: verified\n")
	}
	fmt.Fprintf(out, "  subjects: %d matched, %d mismatched, %d missing, %d extra\n",
		len(c.Matched), len(c.Mismatched), len(c.Missing), len(c.Extra))
	if runFailed {
		fmt.Fprintf(out, "  note: a job failed during the re-run\n")
	}
	if vo.Explain {
		for _, m := range c.Mismatched {
			fmt.Fprintf(out, "  ~ %s\n      expected %s\n      actual   %s\n", m.Name, m.Expected, m.Actual)
		}
		for _, n := range c.Missing {
			fmt.Fprintf(out, "  - missing %s\n", n)
		}
		for _, n := range c.Extra {
			fmt.Fprintf(out, "  + extra   %s\n", n)
		}
	}
	if reportPath != "" {
		fmt.Fprintf(out, "  report: %s\n", reportPath)
	}
}

func modeName(strict bool) string {
	if strict {
		return "strict"
	}
	return "lax"
}

func resultName(verified bool) string {
	if verified {
		return "verified"
	}
	return "failed"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "(none recorded)"
	}
	return s
}
