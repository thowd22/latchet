package mask

import (
	"bytes"
	"testing"
)

func TestSingleWrite(t *testing.T) {
	var b bytes.Buffer
	w := New(&b, []string{"supersekret"})
	w.Write([]byte("VALUE=supersekret done\n"))
	w.Close()
	if got := b.String(); got != "VALUE=*** done\n" {
		t.Errorf("got %q", got)
	}
}

func TestSplitAcrossWrites(t *testing.T) {
	var b bytes.Buffer
	w := New(&b, []string{"supersekret"})
	// "supersekret" arrives split across three writes.
	w.Write([]byte("a=super"))
	w.Write([]byte("sek"))
	w.Write([]byte("ret!\n"))
	w.Close()
	if got := b.String(); got != "a=***!\n" {
		t.Errorf("split mask failed: got %q", got)
	}
}

func TestPartialPrefixIsNotMasked(t *testing.T) {
	var b bytes.Buffer
	w := New(&b, []string{"supersekret"})
	// "super" is a prefix of the secret but never completes — must flush as-is.
	w.Write([]byte("super"))
	w.Write([]byte("man\n"))
	w.Close()
	if got := b.String(); got != "superman\n" {
		t.Errorf("false mask: got %q", got)
	}
}

func TestMultipleSecretsLongestFirst(t *testing.T) {
	var b bytes.Buffer
	w := New(&b, []string{"tok", "token12345"})
	w.Write([]byte("x=token12345 y=tok\n"))
	w.Close()
	if got := b.String(); got != "x=*** y=***\n" {
		t.Errorf("got %q", got)
	}
}

func TestNoSecretsIsPassthroughAndDoesNotCloseUnderlying(t *testing.T) {
	var b bytes.Buffer
	w := New(&b, nil)
	if _, ok := w.(passthrough); !ok {
		t.Fatalf("expected passthrough when no secrets")
	}
	w.Write([]byte("plain text\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if b.String() != "plain text\n" {
		t.Errorf("passthrough altered output: %q", b.String())
	}
}

func TestEmptySecretsFilteredOut(t *testing.T) {
	var b bytes.Buffer
	w := New(&b, []string{"", "real"})
	w.Write([]byte("a=real b=\n"))
	w.Close()
	if got := b.String(); got != "a=*** b=\n" {
		t.Errorf("got %q", got)
	}
}
