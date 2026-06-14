// Package signer optionally signs a latchet provenance file with sigstore
// cosign. cosign is a soft dependency: when it is absent, or no signing key is
// configured, latchet simply leaves the attestation unsigned. Signing is
// always best-effort and never affects a run's exit code.
//
// This package shells out to the cosign CLI (mirroring internal/runtime's
// approach for container runtimes) so it adds no third-party dependency.
package signer

import (
	"context"
	"fmt"
	"os/exec"
)

// binary is the cosign executable name, looked up on PATH.
const binary = "cosign"

// Available reports whether the cosign CLI is on PATH.
func Available() bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

// signBlobArgs builds the cosign argv for key-based blob signing. The output
// is a Sigstore bundle (--bundle), the portable form across cosign v2 and v3
// (v3 deprecated the detached --output-signature). tlog controls whether the
// signature is uploaded to a Rekor transparency log; it defaults off so
// signing works offline and produces no public-log side effects. The cosign
// key's password is read by cosign from the COSIGN_PASSWORD environment
// variable, which the child process inherits.
func signBlobArgs(keyPath, blobPath, bundlePath string, tlog bool) []string {
	args := []string{"sign-blob", "--yes", "--key", keyPath, "--bundle", bundlePath}
	if !tlog {
		// Offline, key-based signing. cosign v3 defaults to a TUF-provided
		// signing config that mandates a transparency-log service;
		// --use-signing-config=false disables it so --tlog-upload=false is
		// honored and no Rekor service is contacted.
		args = append(args, "--use-signing-config=false", "--tlog-upload=false")
	}
	return append(args, blobPath)
}

// SignBlob signs blobPath with the cosign private key at keyPath, writing a
// Sigstore bundle to "<blobPath>.bundle" and returning that path. The caller
// is responsible for checking Available first; a cosign error (including a
// missing binary) is returned with its output for diagnosis. Verify with:
//
//	cosign verify-blob --key <pub> --bundle <blobPath>.bundle \
//	  --insecure-ignore-tlog=true <blobPath>
func SignBlob(ctx context.Context, keyPath, blobPath string, tlog bool) (string, error) {
	bundlePath := blobPath + ".bundle"
	out, err := exec.CommandContext(ctx, binary, signBlobArgs(keyPath, blobPath, bundlePath, tlog)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cosign sign-blob: %w\n%s", err, out)
	}
	return bundlePath, nil
}
