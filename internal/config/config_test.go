package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// write drops content into a temp latchet.yml and returns its path.
func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "latchet.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	wf, err := Load(write(t, `
name: demo
env:
  GLOBAL: g
jobs:
  build:
    container: alpine:3.19
    env:
      STAGE: build
    steps:
      - name: compile
        run: echo hi
  test:
    container: alpine:3.19
    needs: build
    steps:
      - run: echo test
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Name != "demo" {
		t.Errorf("Name = %q, want demo", wf.Name)
	}
	if wf.Jobs["build"].ID != "build" {
		t.Errorf("job ID not populated from map key: %q", wf.Jobs["build"].ID)
	}
	if got := wf.Jobs["build"].Steps[0].Run; got != "echo hi" {
		t.Errorf("step run = %q, want %q", got, "echo hi")
	}
}

func TestNeedsScalarAndList(t *testing.T) {
	wf, err := Load(write(t, `
jobs:
  a:
    container: x
    steps: [{run: echo a}]
  b:
    container: x
    needs: a
    steps: [{run: echo b}]
  c:
    container: x
    needs: [a, b]
    steps: [{run: echo c}]
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual([]string(wf.Jobs["b"].Needs), []string{"a"}) {
		t.Errorf("scalar needs = %v, want [a]", wf.Jobs["b"].Needs)
	}
	if !reflect.DeepEqual([]string(wf.Jobs["c"].Needs), []string{"a", "b"}) {
		t.Errorf("list needs = %v, want [a b]", wf.Jobs["c"].Needs)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	_, err := Load(write(t, `
jobs:
  a:
    container: x
    runs-on: ubuntu-latest
    steps: [{run: echo a}]
`))
	if err == nil {
		t.Fatal("expected unknown key 'runs-on' to be rejected")
	}
	if !strings.Contains(err.Error(), "runs-on") {
		t.Errorf("error should mention the offending key: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantSub string // substring expected in the validation error
	}{
		{
			name:    "missing container",
			yaml:    "jobs:\n  a:\n    steps: [{run: echo a}]\n",
			wantSub: "missing 'container'",
		},
		{
			name:    "no steps",
			yaml:    "jobs:\n  a:\n    container: x\n",
			wantSub: "has no steps",
		},
		{
			name:    "empty run",
			yaml:    "jobs:\n  a:\n    container: x\n    steps: [{run: \"  \"}]\n",
			wantSub: "empty 'run'",
		},
		{
			name:    "unknown need",
			yaml:    "jobs:\n  a:\n    container: x\n    needs: ghost\n    steps: [{run: echo a}]\n",
			wantSub: `needs unknown job "ghost"`,
		},
		{
			name:    "self need",
			yaml:    "jobs:\n  a:\n    container: x\n    needs: a\n    steps: [{run: echo a}]\n",
			wantSub: "cannot depend on itself",
		},
		{
			name:    "inherit unknown",
			yaml:    "jobs:\n  a:\n    container: x\n    steps: [{run: echo a}]\n  b:\n    container: x\n    needs: a\n    inherit: ghost\n    steps: [{run: echo b}]\n",
			wantSub: `inherits unknown job "ghost"`,
		},
		{
			name:    "inherit self",
			yaml:    "jobs:\n  a:\n    container: x\n    inherit: a\n    steps: [{run: echo a}]\n",
			wantSub: "cannot inherit from itself",
		},
		{
			name:    "inherit not in needs",
			yaml:    "jobs:\n  a:\n    container: x\n    steps: [{run: echo a}]\n  b:\n    container: x\n    inherit: a\n    steps: [{run: echo b}]\n",
			wantSub: `inherits from "a" but does not list it in needs`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := Load(write(t, tc.yaml))
			if err != nil {
				t.Fatalf("load failed: %v", err)
			}
			err = wf.Validate()
			if err == nil {
				t.Fatalf("expected validation error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidateCycle(t *testing.T) {
	wf, err := Load(write(t, `
jobs:
  a:
    container: x
    needs: b
    steps: [{run: echo a}]
  b:
    container: x
    needs: a
    steps: [{run: echo b}]
`))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := wf.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got %v", err)
	}
}

func TestValidateOK(t *testing.T) {
	wf, err := Load(write(t, `
jobs:
  a:
    container: x
    steps: [{run: echo a}]
  b:
    container: x
    needs: a
    steps: [{name: t, run: echo b}]
`))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("expected valid workflow, got %v", err)
	}
}

func TestValidateInheritOK(t *testing.T) {
	wf, err := Load(write(t, `
jobs:
  parent:
    container: x
    steps: [{run: echo p}]
  child:
    container: x
    needs: parent
    inherit: parent
    steps: [{run: echo c}]
`))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("expected valid workflow, got %v", err)
	}
	if got := wf.Jobs["child"].Inherit; got != "parent" {
		t.Errorf("Inherit not populated: got %q, want %q", got, "parent")
	}
}
