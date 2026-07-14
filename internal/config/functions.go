package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thowd22/latchet/internal/cond"
)

// validateSteps validates an ordered step list (a job's steps or a function's
// body) and returns any problems. ctx labels the owner for messages (e.g.
// `job "a"` or `function "f"`). fns is the resolved function set used to check
// `call:` references; keys is the resolved key set (fetched functions, keyed
// by verbatim uses string) used to check `uses:` references; allowInvokes is
// false for function and key bodies (no nesting).
func validateSteps(ctx string, steps []*Step, fns, keys map[string]*Function, allowInvokes bool) []string {
	var errs []string
	chainOpen := false // a preceding if/elif a following elif/else can attach to
	for i, step := range steps {
		n := i + 1
		if step == nil {
			errs = append(errs, fmt.Sprintf("%s: step %d is empty", ctx, n))
			continue
		}
		isCall := step.Call != ""
		isUses := step.Uses != ""
		hasRun := strings.TrimSpace(step.Run) != ""

		if isCall && hasRun {
			errs = append(errs, fmt.Sprintf("%s: step %d has both 'run' and 'call'", ctx, n))
		}
		if isUses && hasRun {
			errs = append(errs, fmt.Sprintf("%s: step %d has both 'run' and 'uses'", ctx, n))
		}
		if isCall && isUses {
			errs = append(errs, fmt.Sprintf("%s: step %d has both 'call' and 'uses'", ctx, n))
		}
		if isCall {
			errs = append(errs, validateCall(ctx, n, step, fns, allowInvokes)...)
			chainOpen = false // a call is not part of a conditional chain
			continue
		}
		if isUses {
			errs = append(errs, validateUses(ctx, n, step, keys, allowInvokes)...)
			chainOpen = false // a uses step is not part of a conditional chain
			continue
		}
		if !hasRun {
			errs = append(errs, fmt.Sprintf("%s: step %d has an empty 'run'", ctx, n))
			continue
		}

		// At most one of if/elif/else.
		set := 0
		if step.If != "" {
			set++
		}
		if step.Elif != "" {
			set++
		}
		if step.Else {
			set++
		}
		if set > 1 {
			errs = append(errs, fmt.Sprintf("%s: step %d uses more than one of if/elif/else", ctx, n))
		}
		switch step.kind() {
		case stepIf:
			if err := cond.Check(step.If); err != nil {
				errs = append(errs, fmt.Sprintf("%s: step %d if: %v", ctx, n, err))
			}
			chainOpen = true
		case stepElif:
			if !chainOpen {
				errs = append(errs, fmt.Sprintf("%s: step %d elif: must follow an if/elif step", ctx, n))
			}
			if err := cond.Check(step.Elif); err != nil {
				errs = append(errs, fmt.Sprintf("%s: step %d elif: %v", ctx, n, err))
			}
		case stepElse:
			if !chainOpen {
				errs = append(errs, fmt.Sprintf("%s: step %d else: must follow an if/elif step", ctx, n))
			}
			chainOpen = false // else closes the chain
		default: // plain run step ends any chain
			chainOpen = false
		}
	}
	return errs
}

func validateCall(ctx string, n int, step *Step, fns map[string]*Function, allowCalls bool) []string {
	var errs []string
	if !allowCalls {
		errs = append(errs, fmt.Sprintf("%s: step %d call %q: a function cannot call another function", ctx, n, step.Call))
	}
	if step.If != "" || step.Elif != "" || step.Else {
		errs = append(errs, fmt.Sprintf("%s: step %d: a call step cannot have if/elif/else", ctx, n))
	}
	fn := fns[step.Call]
	if fn == nil {
		errs = append(errs, fmt.Sprintf("%s: step %d calls unknown function %q", ctx, n, step.Call))
		return errs
	}
	return append(errs, validateWith(ctx, n, "function", step.Call, fn, step.With)...)
}

func validateUses(ctx string, n int, step *Step, keys map[string]*Function, allowUses bool) []string {
	var errs []string
	if !allowUses {
		errs = append(errs, fmt.Sprintf("%s: step %d uses %q: a function cannot use a key", ctx, n, step.Uses))
	}
	if step.If != "" || step.Elif != "" || step.Else {
		errs = append(errs, fmt.Sprintf("%s: step %d: a uses step cannot have if/elif/else", ctx, n))
	}
	fn := keys[step.Uses]
	if fn == nil {
		errs = append(errs, fmt.Sprintf("%s: step %d: key %q not resolved", ctx, n, step.Uses))
		return errs
	}
	return append(errs, validateWith(ctx, n, "key", step.Uses, fn, step.With)...)
}

// validateWith checks a call/uses step's `with:` map against the invoked
// function's declared inputs: every with key must be a declared input, and
// every required input must be provided. kind labels messages ("function" or
// "key"); name is the function name or verbatim uses string.
func validateWith(ctx string, n int, kind, name string, fn *Function, with map[string]string) []string {
	var errs []string
	// `with` keys must be declared inputs.
	wkeys := make([]string, 0, len(with))
	for k := range with {
		wkeys = append(wkeys, k)
	}
	sort.Strings(wkeys)
	for _, k := range wkeys {
		if fn.Inputs[k] == nil {
			errs = append(errs, fmt.Sprintf("%s: step %d: %q is not an input of %s %q", ctx, n, k, kind, name))
		}
	}
	// Required inputs must be provided.
	inkeys := make([]string, 0, len(fn.Inputs))
	for k := range fn.Inputs {
		inkeys = append(inkeys, k)
	}
	sort.Strings(inkeys)
	for _, in := range inkeys {
		if fn.Inputs[in].Required {
			if _, ok := with[in]; !ok {
				errs = append(errs, fmt.Sprintf("%s: step %d: %s %q requires input %q", ctx, n, kind, name, in))
			}
		}
	}
	return errs
}

// MergeFunctions overlays a workflow's local functions onto a set of global
// functions, returning the effective set (local shadows global by name). The
// inputs are not mutated.
func MergeFunctions(global, local map[string]*Function) map[string]*Function {
	if len(global) == 0 {
		return local
	}
	out := make(map[string]*Function, len(global)+len(local))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range local {
		out[k] = v
	}
	return out
}

// ExpandCalls returns steps with every `call:` step replaced by the called
// function's steps and every `uses:` step replaced by the resolved key's
// steps. expand is applied to each input value (the step's `with:` value, or
// the input's default) so inputs may reference the caller's env; inputs are
// injected as env vars below each function step's own env. Assumes the
// workflow validated, so every call and key resolves.
func ExpandCalls(steps []*Step, fns, keys map[string]*Function, expand func(string) string) []*Step {
	out := make([]*Step, 0, len(steps))
	for _, step := range steps {
		var fn *Function
		switch {
		case step.Call != "":
			fn = fns[step.Call]
		case step.Uses != "":
			fn = keys[step.Uses]
		default:
			out = append(out, step)
			continue
		}
		if fn == nil { // defensive; validation should have caught it
			continue
		}
		inputEnv := map[string]string{}
		for name, spec := range fn.Inputs {
			val := spec.Default
			if v, ok := step.With[name]; ok {
				val = v
			}
			inputEnv[name] = expand(val)
		}
		for _, fs := range fn.Steps {
			cp := *fs
			cp.Env = mergeStringMaps(inputEnv, fs.Env) // step env wins over inputs
			out = append(out, &cp)
		}
	}
	return out
}

func mergeStringMaps(low, high map[string]string) map[string]string {
	if len(low) == 0 {
		return high
	}
	out := make(map[string]string, len(low)+len(high))
	for k, v := range low {
		out[k] = v
	}
	for k, v := range high {
		out[k] = v
	}
	return out
}
