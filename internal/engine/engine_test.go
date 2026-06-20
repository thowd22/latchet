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
