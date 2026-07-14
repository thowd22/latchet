package config

import (
	"strings"
	"testing"
)

func TestValidateFunctions(t *testing.T) {
	cases := []struct {
		name, yaml, wantSub string
	}{
		{
			"unknown function",
			"jobs:\n  a:\n    container: x\n    steps:\n      - {call: nope}\n",
			`calls unknown function "nope"`,
		},
		{
			"missing required input",
			"name: x\nfunctions:\n  f:\n    inputs:\n      need: {required: true}\n    steps: [{run: echo}]\njobs:\n  a:\n    container: x\n    steps:\n      - {call: f}\n",
			`requires input "need"`,
		},
		{
			"undeclared with key",
			"name: x\nfunctions:\n  f:\n    steps: [{run: echo}]\njobs:\n  a:\n    container: x\n    steps:\n      - {call: f, with: {bogus: 1}}\n",
			`"bogus" is not an input of function "f"`,
		},
		{
			"run and call together",
			"name: x\nfunctions:\n  f:\n    steps: [{run: echo}]\njobs:\n  a:\n    container: x\n    steps:\n      - {call: f, run: echo hi}\n",
			"both 'run' and 'call'",
		},
		{
			"nested call forbidden",
			"name: x\nfunctions:\n  f:\n    steps: [{run: echo}]\n  g:\n    steps:\n      - {call: f}\njobs:\n  a:\n    container: x\n    steps:\n      - {call: g}\n",
			"a function cannot call another function",
		},
		{
			"call with if rejected",
			"name: x\nfunctions:\n  f:\n    steps: [{run: echo}]\njobs:\n  a:\n    container: x\n    steps:\n      - {call: f, if: \"$X == 1\"}\n",
			"a call step cannot have if/elif/else",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := Load(write(t, tc.yaml))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			err = wf.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestExpandCalls(t *testing.T) {
	fns := map[string]*Function{
		"greet": {
			Inputs: map[string]*Input{"who": {Required: true}, "punct": {Default: "!"}},
			Steps:  []*Step{{Run: "echo hi $who$punct"}},
		},
	}
	steps := []*Step{
		{Run: "echo before"},
		{Call: "greet", With: map[string]string{"who": "$LOC"}},
		{Call: "greet", With: map[string]string{"who": "x", "punct": "?"}},
	}
	expand := func(v string) string { return ExpandVars(v, map[string]string{"LOC": "server"}) }
	out := ExpandCalls(steps, fns, nil, expand)

	if len(out) != 3 { // 1 literal + 1 + 1 function steps
		t.Fatalf("expected 3 steps, got %d", len(out))
	}
	if out[0].Run != "echo before" {
		t.Errorf("literal step changed: %q", out[0].Run)
	}
	// first call: who expanded from $LOC, punct default "!"
	if out[1].Env["who"] != "server" || out[1].Env["punct"] != "!" {
		t.Errorf("call 1 env wrong: %v", out[1].Env)
	}
	// second call: explicit values
	if out[2].Env["who"] != "x" || out[2].Env["punct"] != "?" {
		t.Errorf("call 2 env wrong: %v", out[2].Env)
	}
}

const testKeyRef = "git@example.com:me/keys//greet@v1"

func TestValidateUses(t *testing.T) {
	usesYAML := func(step string) string {
		return "jobs:\n  a:\n    container: x\n    steps:\n      - " + step + "\n"
	}
	greet := &Function{
		Inputs: map[string]*Input{"who": {Required: true}, "punct": {Default: "!"}},
		Steps:  []*Step{{Run: "echo hi $who$punct"}},
	}
	cases := []struct {
		name, yaml string
		keys       map[string]*Function
		wantSub    string // "" = expect valid
	}{
		{
			"valid uses",
			usesYAML(`{uses: "` + testKeyRef + `", with: {who: x}}`),
			map[string]*Function{testKeyRef: greet},
			"",
		},
		{
			"unresolved key",
			usesYAML(`{uses: "` + testKeyRef + `", with: {who: x}}`),
			nil,
			`key "` + testKeyRef + `" not resolved`,
		},
		{
			"missing required input",
			usesYAML(`{uses: "` + testKeyRef + `"}`),
			map[string]*Function{testKeyRef: greet},
			`key "` + testKeyRef + `" requires input "who"`,
		},
		{
			"undeclared with key",
			usesYAML(`{uses: "` + testKeyRef + `", with: {who: x, bogus: 1}}`),
			map[string]*Function{testKeyRef: greet},
			`"bogus" is not an input of key`,
		},
		{
			"run and uses together",
			usesYAML(`{uses: "` + testKeyRef + `", run: echo hi, with: {who: x}}`),
			map[string]*Function{testKeyRef: greet},
			"both 'run' and 'uses'",
		},
		{
			"call and uses together",
			"functions:\n  f:\n    steps: [{run: echo}]\n" + usesYAML(`{uses: "`+testKeyRef+`", call: f, with: {who: x}}`),
			map[string]*Function{testKeyRef: greet},
			"both 'call' and 'uses'",
		},
		{
			"uses with if rejected",
			usesYAML(`{uses: "`+testKeyRef+`", with: {who: x}, if: "$X == 1"}`),
			map[string]*Function{testKeyRef: greet},
			"a uses step cannot have if/elif/else",
		},
		{
			"uses inside a function body",
			"functions:\n  g:\n    steps:\n      - {uses: \"" + testKeyRef + "\", with: {who: x}}\njobs:\n  a:\n    container: x\n    steps:\n      - {call: g}\n",
			map[string]*Function{testKeyRef: greet},
			"a function cannot use a key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := Load(write(t, tc.yaml))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			wf.Keys = tc.keys
			err = wf.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestExpandCallsUses(t *testing.T) {
	keys := map[string]*Function{
		testKeyRef: {
			Inputs: map[string]*Input{"who": {Required: true}, "punct": {Default: "!"}},
			Steps:  []*Step{{Run: "echo hi $who$punct"}},
		},
	}
	steps := []*Step{
		{Run: "echo before"},
		{Uses: testKeyRef, With: map[string]string{"who": "$LOC"}},
	}
	expand := func(v string) string { return ExpandVars(v, map[string]string{"LOC": "server"}) }
	out := ExpandCalls(steps, nil, keys, expand)

	if len(out) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(out))
	}
	if out[1].Run != "echo hi $who$punct" {
		t.Errorf("key step not inlined: %q", out[1].Run)
	}
	if out[1].Env["who"] != "server" || out[1].Env["punct"] != "!" {
		t.Errorf("key input env wrong: %v", out[1].Env)
	}
}

func TestMergeFunctionsLocalShadowsGlobal(t *testing.T) {
	global := map[string]*Function{
		"a": {Steps: []*Step{{Run: "global a"}}},
		"b": {Steps: []*Step{{Run: "global b"}}},
	}
	local := map[string]*Function{
		"b": {Steps: []*Step{{Run: "local b"}}},
	}
	out := MergeFunctions(global, local)
	if out["a"].Steps[0].Run != "global a" {
		t.Errorf("global-only function lost")
	}
	if out["b"].Steps[0].Run != "local b" {
		t.Errorf("local did not shadow global")
	}
}
