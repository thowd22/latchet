package globalconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadViaLATCHET_CONFIG(t *testing.T) {
	p := writeConfig(t, `
runtime: podman
workspace_root: /tmp/ws
log_dir: /tmp/logs
max_parallel: 3
env:
  CI: "true"
  REGISTRY: ghcr.io/me
watch:
  - url: git@github.com:me/app.git
    branches: [main, release]
    tags: ["v*"]
`)
	t.Setenv("LATCHET_CONFIG", p)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Runtime != "podman" || c.WorkspaceRoot != "/tmp/ws" || c.MaxParallel != 3 {
		t.Errorf("scalars wrong: %+v", c)
	}
	if c.Env["CI"] != "true" || c.Env["REGISTRY"] != "ghcr.io/me" {
		t.Errorf("env wrong: %v", c.Env)
	}
	if len(c.Watch) != 1 || c.Watch[0].URL != "git@github.com:me/app.git" ||
		len(c.Watch[0].Branches) != 2 || len(c.Watch[0].Tags) != 1 {
		t.Errorf("watch wrong: %+v", c.Watch)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoadNoFileIsEmpty(t *testing.T) {
	t.Setenv("LATCHET_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty dir, no config
	t.Setenv("HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatalf("Load with no file should not error: %v", err)
	}
	if c.Path != "" || c.Runtime != "" || len(c.Watch) != 0 {
		t.Errorf("expected empty config, got %+v", c)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	p := writeConfig(t, "runtime: podman\nbogus: 1\n")
	t.Setenv("LATCHET_CONFIG", p)
	if _, err := Load(); err == nil {
		t.Fatal("expected unknown key to be rejected")
	}
}

func TestValidate(t *testing.T) {
	bad := &Config{Runtime: "containerd", MaxParallel: -1, Watch: []WatchEntry{{URL: ""}, {URL: "x"}}}
	err := bad.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"runtime", "max_parallel", "watch[0]", "watch[1]"} {
		if !contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestApplyEnvDefaultsRespectsExisting(t *testing.T) {
	t.Setenv("LATCHET_RUNTIME", "docker") // already set -> must win
	t.Setenv("LATCHET_WORKSPACE_ROOT", "")
	c := &Config{Runtime: "podman", WorkspaceRoot: "/from/config"}
	c.ApplyEnvDefaults()
	if os.Getenv("LATCHET_RUNTIME") != "docker" {
		t.Errorf("existing env var must win, got %q", os.Getenv("LATCHET_RUNTIME"))
	}
	if os.Getenv("LATCHET_WORKSPACE_ROOT") != "/from/config" {
		t.Errorf("unset var should take config value, got %q", os.Getenv("LATCHET_WORKSPACE_ROOT"))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
