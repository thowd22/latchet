package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadEnvFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "env")

	// Missing file -> no outputs, no error.
	if m, err := readEnvFile(p); err != nil || len(m) != 0 {
		t.Fatalf("missing file: %v %v", m, err)
	}

	body := "" +
		"VERSION=1.2.3\n" +
		"URL=https://x/y?a=b=c\n" + // value may contain '='
		"\n" + // blank line ignored
		"no_equals_line\n" + // ignored
		"  SPACED  =  trim key only \n" + // key trimmed, value kept verbatim
		"1BAD=x\n" + // invalid name (leading digit) ignored
		"VERSION=4.5.6\n" // later wins
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"VERSION": "4.5.6",
		"URL":     "https://x/y?a=b=c",
		"SPACED":  "  trim key only ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readEnvFile = %#v, want %#v", got, want)
	}
}

func TestContainerName(t *testing.T) {
	// A matrix job ID with spaces/parens/= must yield a runtime-safe name, and
	// distinct IDs must stay distinct.
	a := containerName("run1", "build (arch=amd64, target=linux)")
	b := containerName("run1", "build (arch=arm64, target=linux)")
	if a == b {
		t.Fatal("distinct job IDs produced the same container name")
	}
	for _, name := range []string{a, b} {
		for _, r := range name {
			ok := r == '_' || r == '.' || r == '-' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !ok {
				t.Errorf("container name %q has invalid char %q", name, r)
			}
		}
	}
	if got := containerName("run1", "build"); got != "latchet-run1-build" {
		t.Errorf("plain job: got %q", got)
	}
}

func TestValidEnvName(t *testing.T) {
	ok := []string{"A", "_x", "FOO_BAR", "x9", "LATCHET_GIT_SHA"}
	bad := []string{"", "9x", "a-b", "a.b", "a b", "a=b"}
	for _, s := range ok {
		if !validEnvName(s) {
			t.Errorf("validEnvName(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validEnvName(s) {
			t.Errorf("validEnvName(%q) = true, want false", s)
		}
	}
}
