package config

import (
	"fmt"
	"sort"
	"strings"
)

// ExpandMatrix returns a copy of wf with every `strategy.matrix` job replaced
// by one job per combination of matrix values. It runs after Validate (which
// checks matrix sanity) and before the DAG is built, so the rest of the engine
// never sees `strategy`.
//
// For a matrix job `test` with matrix {go: [1.21, 1.22]}, the expansion creates
// jobs `test (go=1.21)` and `test (go=1.22)`, each with its matrix variables set
// as env (and `$VAR`-expanded into the `container:` field). Any job that
// `needs:` the original is rewritten to need every expansion.
func ExpandMatrix(wf *Workflow) *Workflow {
	// Map each matrix job's original id to its expanded ids (sorted).
	expanded := map[string][]string{}
	for id, job := range wf.Jobs {
		if job.Strategy != nil && len(job.Strategy.Matrix) > 0 {
			expanded[id] = nil // filled below
		}
	}
	if len(expanded) == 0 {
		return wf
	}

	out := &Workflow{
		Name:          wf.Name,
		Env:           wf.Env,
		Deterministic: wf.Deterministic,
		Secrets:       wf.Secrets,
		Functions:     wf.Functions,
		Keys:          wf.Keys,
		Jobs:          make(map[string]*Job, len(wf.Jobs)),
	}

	for id, job := range wf.Jobs {
		if _, isMatrix := expanded[id]; !isMatrix {
			continue // copied (with rewritten needs) in the second pass
		}
		for _, combo := range matrixCombos(job.Strategy.Matrix) {
			nj := cloneJobForMatrix(job, combo)
			nj.ID = id + " " + comboLabel(combo)
			out.Jobs[nj.ID] = nj
			expanded[id] = append(expanded[id], nj.ID)
		}
		sort.Strings(expanded[id])
	}

	// Second pass: copy non-matrix jobs, and rewrite every job's needs so a
	// reference to a matrix job becomes references to all its expansions.
	rewrite := func(needs StringOrSlice) StringOrSlice {
		var ns []string
		for _, n := range needs {
			if ex, ok := expanded[n]; ok {
				ns = append(ns, ex...)
			} else {
				ns = append(ns, n)
			}
		}
		return StringOrSlice(ns)
	}
	for id, job := range wf.Jobs {
		if _, isMatrix := expanded[id]; !isMatrix {
			nj := *job
			nj.Needs = rewrite(job.Needs)
			out.Jobs[id] = &nj
		}
	}
	for _, nj := range out.Jobs {
		if nj.Strategy != nil { // an expansion: rewrite its (inherited) needs too
			nj.Needs = rewrite(nj.Needs)
			nj.Strategy = nil
		}
	}
	return out
}

// cloneJobForMatrix copies job and applies one matrix combination: matrix vars
// are merged into the job's env (overriding) and expanded into container.
func cloneJobForMatrix(job *Job, combo map[string]string) *Job {
	nj := *job
	env := make(map[string]string, len(job.Env)+len(combo))
	for k, v := range job.Env {
		env[k] = v
	}
	for k, v := range combo {
		env[k] = v
	}
	nj.Env = env
	nj.Container = expandVars(job.Container, combo)
	// Strategy is cleared by the caller after needs-rewrite; keep it set here so
	// the second pass can tell expansions apart from plain jobs.
	return &nj
}

// matrixCombos returns the cartesian product of the matrix as a list of
// name->value maps, in a deterministic order (matrix keys sorted, then values
// in declared order).
func matrixCombos(matrix map[string][]string) []map[string]string {
	keys := make([]string, 0, len(matrix))
	for k := range matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	combos := []map[string]string{{}}
	for _, k := range keys {
		var next []map[string]string
		for _, base := range combos {
			for _, v := range matrix[k] {
				m := make(map[string]string, len(base)+1)
				for bk, bv := range base {
					m[bk] = bv
				}
				m[k] = v
				next = append(next, m)
			}
		}
		combos = next
	}
	return combos
}

// comboLabel renders a combination as "(k1=v1, k2=v2)" with keys sorted.
func comboLabel(combo map[string]string) string {
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%s", k, combo[k])
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// ExpandVars replaces $NAME and ${NAME} in s with vars[NAME] (names absent from
// vars are left untouched). Exported for reuse expanding function `with:` inputs.
func ExpandVars(s string, vars map[string]string) string { return expandVars(s, vars) }

// expandVars replaces $NAME and ${NAME} in s with vars[NAME], for names present
// in vars (others are left untouched). Used to vary the container image by
// matrix value, e.g. `golang:${go}`.
func expandVars(s string, vars map[string]string) string {
	if !strings.Contains(s, "$") || len(vars) == 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 1
		braced := j < len(s) && s[j] == '{'
		if braced {
			j++
		}
		start := j
		for j < len(s) && isNameByte(s[j]) {
			j++
		}
		name := s[start:j]
		if braced {
			if j < len(s) && s[j] == '}' {
				j++
			} else {
				name = "" // unterminated ${ ; leave literal
			}
		}
		if v, ok := vars[name]; ok && name != "" {
			b.WriteString(v)
		} else {
			b.WriteString(s[i:j]) // leave the reference as written
		}
		i = j
	}
	return b.String()
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
