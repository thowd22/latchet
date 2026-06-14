package signer

import (
	"reflect"
	"testing"
)

func TestSignBlobArgs(t *testing.T) {
	got := signBlobArgs("/k/cosign.key", "/logs/provenance.json", "/logs/provenance.json.bundle", false)
	want := []string{
		"sign-blob", "--yes",
		"--key", "/k/cosign.key",
		"--bundle", "/logs/provenance.json.bundle",
		"--use-signing-config=false", "--tlog-upload=false",
		"/logs/provenance.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("signBlobArgs (tlog off) =\n  %v\nwant\n  %v", got, want)
	}
}

func TestSignBlobArgsWithTlog(t *testing.T) {
	got := signBlobArgs("k", "b", "b.bundle", true)
	// With tlog on, latchet must not force the offline knobs — cosign uploads
	// to Rekor using its defaults.
	for _, a := range got {
		if a == "--tlog-upload=false" || a == "--use-signing-config=false" {
			t.Fatalf("tlog enabled should not pass offline flags: %v", got)
		}
	}
	// blob path is always last.
	if got[len(got)-1] != "b" {
		t.Errorf("blob path must be last arg, got %v", got)
	}
}
