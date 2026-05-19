// Package version exposes the build-time version metadata stamped into the
// binary by the release pipeline.
//
// The defaults below are placeholders for unstamped development builds. The
// release workflow overrides them via:
//
//	go build -ldflags "\
//	  -X github.com/thowd22/latchet/internal/version.Version=$TAG \
//	  -X github.com/thowd22/latchet/internal/version.Commit=$SHA"
package version

// Version is the release tag, e.g. "v0.2.0". "dev" for un-stamped builds.
var Version = "dev"

// Commit is the short git SHA the binary was built from. "unknown" for
// un-stamped builds.
var Commit = "unknown"
