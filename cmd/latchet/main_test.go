package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thowd22/latchet/internal/engine"
)

// Both -help and -h must print usage to stdout and exit 0. -help was
// previously undefined, so it fell through to a flag-parse error (exit 2)
// even though the usage text advertised it.
func TestHelpFlagsExitZero(t *testing.T) {
	for _, arg := range []string{"-help", "-h"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{arg}, &stdout, &stderr)
		if code != engine.ExitSuccess {
			t.Errorf("run(%q) = %d, want %d; stderr=%q", arg, code, engine.ExitSuccess, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Errorf("run(%q): usage not printed to stdout, got %q", arg, stdout.String())
		}
	}
}

func TestVersionFlagExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version"}, &stdout, &stderr); code != engine.ExitSuccess {
		t.Fatalf("run(-version) = %d, want %d", code, engine.ExitSuccess)
	}
	if !strings.HasPrefix(stdout.String(), "latchet ") {
		t.Errorf("run(-version): got %q", stdout.String())
	}
}

// An unknown flag is a config error (exit 2), distinct from -help.
func TestUnknownFlagExitsConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-nonexistent"}, &stdout, &stderr); code != engine.ExitConfig {
		t.Fatalf("run(-nonexistent) = %d, want %d", code, engine.ExitConfig)
	}
}
