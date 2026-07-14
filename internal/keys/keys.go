// Package keys resolves `uses:` key references: fetched, reusable functions
// living in remote git repos. A reference names a git URL, an optional
// directory inside the repo, and a pinned ref:
//
//	<git url>[//<subpath>]@<ref>
//
// e.g. git@github.com:thowd22/latchet-keys//checkout@v1. The ref must be a
// tag or a full 40-hex commit SHA — branches are rejected so runs stay
// reproducible. The named directory (repo root when //<subpath> is omitted)
// must contain a key.yml with the same inputs/steps shape as a workflow
// function.
package keys

import (
	"fmt"
	"path"
	"strings"
)

// Ref is a parsed uses: reference.
type Ref struct {
	Raw     string // the uses: string as written
	URL     string // git clone URL
	Subpath string // slash path inside the repo; "" = repo root
	RefName string // tag name or 40-hex commit SHA
}

// IsSHA reports whether s is a full 40-hex (lowercase) commit SHA.
func IsSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// validRefName reports whether s is acceptable as a pinned ref: a tag-safe
// name ([A-Za-z0-9._-]+) or a full SHA. Slashes and colons are rejected —
// they signal a malformed reference (usually a missing @<ref>).
func validRefName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// ParseRef parses a uses: reference of the form <git url>[//<subpath>]@<ref>.
// The //-separator is searched after any URL scheme (so https:// works); the
// ref is everything after the last @ following the URL (so scp-style
// git@host: URLs work).
func ParseRef(s string) (Ref, error) {
	ref := Ref{Raw: s}
	if strings.TrimSpace(s) == "" {
		return ref, fmt.Errorf("empty key reference")
	}

	// Skip a scheme's own // (https://, ssh://, file://) when locating the
	// url//subpath separator.
	searchFrom := 0
	if i := strings.Index(s, "://"); i >= 0 {
		searchFrom = i + len("://")
	}

	rest := s
	if i := strings.Index(s[searchFrom:], "//"); i >= 0 {
		sep := searchFrom + i
		ref.URL = s[:sep]
		rest = s[sep+2:]
		at := strings.LastIndex(rest, "@")
		if at < 0 {
			return ref, fmt.Errorf("key %q: missing @<ref> (pin to a tag or commit SHA)", s)
		}
		ref.Subpath, ref.RefName = rest[:at], rest[at+1:]
		if ref.Subpath == "" {
			return ref, fmt.Errorf("key %q: empty subpath after //", s)
		}
		if strings.Contains(ref.Subpath, `\`) || strings.HasPrefix(ref.Subpath, "/") {
			return ref, fmt.Errorf("key %q: invalid subpath %q", s, ref.Subpath)
		}
		clean := path.Clean(ref.Subpath)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return ref, fmt.Errorf("key %q: subpath %q escapes the repository", s, ref.Subpath)
		}
		ref.Subpath = clean
	} else {
		at := strings.LastIndex(rest, "@")
		if at < 0 {
			return ref, fmt.Errorf("key %q: missing @<ref> (pin to a tag or commit SHA)", s)
		}
		ref.URL, ref.RefName = rest[:at], rest[at+1:]
	}

	if strings.TrimSpace(ref.URL) == "" {
		return ref, fmt.Errorf("key %q: missing git URL", s)
	}
	if !validRefName(ref.RefName) {
		return ref, fmt.Errorf("key %q: missing or invalid @<ref> (pin to a tag or commit SHA)", s)
	}
	return ref, nil
}

// resolvedURI is the provenance form of a fetched key:
// git+<url>[//<subpath>]@<sha>.
func (r Ref) resolvedURI(sha string) string {
	uri := "git+" + r.URL
	if r.Subpath != "" {
		uri += "//" + r.Subpath
	}
	return uri + "@" + sha
}
