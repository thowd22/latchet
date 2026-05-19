package runtime

import (
	"reflect"
	"testing"
)

func TestCreateArgs(t *testing.T) {
	got := createArgs("latchet-run1-build", "alpine:3.19", "/tmp/latchet/run1/build")
	want := []string{
		"create",
		"--name", "latchet-run1-build",
		"-w", "/workspace",
		"-v", "/tmp/latchet/run1/build:/workspace",
		"alpine:3.19",
		"sh", "-c", "sleep infinity",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("createArgs = %v, want %v", got, want)
	}
}

func TestExecArgs(t *testing.T) {
	got := execArgs("c1", []string{"A=1", "B=2"}, "echo hi")
	want := []string{
		"exec", "-w", "/workspace",
		"-e", "A=1",
		"-e", "B=2",
		"c1", "sh", "-c", "set -e\necho hi",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execArgs = %v, want %v", got, want)
	}
}

func TestExecArgsNoEnv(t *testing.T) {
	got := execArgs("c1", nil, "true")
	want := []string{"exec", "-w", "/workspace", "c1", "sh", "-c", "set -e\ntrue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("execArgs = %v, want %v", got, want)
	}
}

func TestSimpleArgs(t *testing.T) {
	if got := rmArgs("c1"); !reflect.DeepEqual(got, []string{"rm", "-f", "c1"}) {
		t.Errorf("rmArgs = %v", got)
	}
	if got := startArgs("c1"); !reflect.DeepEqual(got, []string{"start", "c1"}) {
		t.Errorf("startArgs = %v", got)
	}
	if got := inspectArgs("img"); !reflect.DeepEqual(got, []string{"image", "inspect", "img"}) {
		t.Errorf("inspectArgs = %v", got)
	}
	if got := pullArgs("img"); !reflect.DeepEqual(got, []string{"pull", "img"}) {
		t.Errorf("pullArgs = %v", got)
	}
}

func TestDetect(t *testing.T) {
	have := func(names ...string) func(string) (string, bool) {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return func(name string) (string, bool) {
			return "/usr/bin/" + name, set[name]
		}
	}

	t.Run("prefers docker", func(t *testing.T) {
		got, err := detect("", have("docker", "podman"))
		if err != nil || got != "docker" {
			t.Fatalf("detect = %q, %v; want docker", got, err)
		}
	})
	t.Run("falls back to podman", func(t *testing.T) {
		got, err := detect("", have("podman"))
		if err != nil || got != "podman" {
			t.Fatalf("detect = %q, %v; want podman", got, err)
		}
	})
	t.Run("override honored", func(t *testing.T) {
		got, err := detect("podman", have("docker", "podman"))
		if err != nil || got != "podman" {
			t.Fatalf("detect = %q, %v; want podman", got, err)
		}
	})
	t.Run("override missing errors", func(t *testing.T) {
		if _, err := detect("nerdctl", have("docker")); err == nil {
			t.Fatal("expected error for missing override binary")
		}
	})
	t.Run("none found errors", func(t *testing.T) {
		if _, err := detect("", have()); err == nil {
			t.Fatal("expected error when no runtime present")
		}
	})
}
