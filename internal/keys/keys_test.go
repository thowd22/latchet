package keys

import (
	"strings"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestParseRef(t *testing.T) {
	cases := []struct {
		name, in string
		want     Ref    // zero-valued when wantErr is set
		wantErr  string // substring of the expected error
	}{
		{
			"scp form with subpath",
			"git@github.com:me/keys//checkout@v1",
			Ref{URL: "git@github.com:me/keys", Subpath: "checkout", RefName: "v1"},
			"",
		},
		{
			"scp form nested subpath",
			"git@github.com:me/keys//build/go@v1.2.0",
			Ref{URL: "git@github.com:me/keys", Subpath: "build/go", RefName: "v1.2.0"},
			"",
		},
		{
			"scp form no subpath",
			"git@github.com:me/keys@v1",
			Ref{URL: "git@github.com:me/keys", Subpath: "", RefName: "v1"},
			"",
		},
		{
			"https form with subpath",
			"https://github.com/me/keys//checkout@v1",
			Ref{URL: "https://github.com/me/keys", Subpath: "checkout", RefName: "v1"},
			"",
		},
		{
			"https form no subpath",
			"https://github.com/me/keys@v1",
			Ref{URL: "https://github.com/me/keys", Subpath: "", RefName: "v1"},
			"",
		},
		{
			"local path with subpath",
			"/srv/git/keys//greet@v1",
			Ref{URL: "/srv/git/keys", Subpath: "greet", RefName: "v1"},
			"",
		},
		{
			"sha ref",
			"git@github.com:me/keys//checkout@" + sha,
			Ref{URL: "git@github.com:me/keys", Subpath: "checkout", RefName: sha},
			"",
		},
		{"empty", "", Ref{}, "empty key reference"},
		{"missing ref scp", "git@github.com:me/keys", Ref{}, "missing or invalid @<ref>"},
		{"missing ref https", "https://github.com/me/keys", Ref{}, "missing @<ref>"},
		{"missing ref with subpath", "git@github.com:me/keys//checkout", Ref{}, "missing @<ref>"},
		{"empty subpath", "git@github.com:me/keys//@v1", Ref{}, "empty subpath"},
		{"traversal subpath", "git@github.com:me/keys//../escape@v1", Ref{}, "escapes the repository"},
		{"nested traversal subpath", "git@github.com:me/keys//a/../../b@v1", Ref{}, "escapes the repository"},
		{"absolute subpath", "git@github.com:me/keys///etc@v1", Ref{}, "invalid subpath"},
		{"backslash subpath", `git@github.com:me/keys//a\b@v1`, Ref{}, "invalid subpath"},
		{"ref with slash", "git@github.com:me/keys//checkout@release/v1", Ref{}, "missing or invalid @<ref>"},
		{"missing url", "//checkout@v1", Ref{}, "missing git URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRef(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.want.Raw = tc.in
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIsSHA(t *testing.T) {
	if !IsSHA(sha) {
		t.Errorf("IsSHA(%q) = false, want true", sha)
	}
	for _, s := range []string{"", "v1", sha[:39], sha + "0", strings.ToUpper(sha), "g" + sha[1:]} {
		if IsSHA(s) {
			t.Errorf("IsSHA(%q) = true, want false", s)
		}
	}
}

func TestResolvedURI(t *testing.T) {
	r := Ref{URL: "git@github.com:me/keys", Subpath: "checkout"}
	if got, want := r.resolvedURI(sha), "git+git@github.com:me/keys//checkout@"+sha; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	r.Subpath = ""
	if got, want := r.resolvedURI(sha), "git+git@github.com:me/keys@"+sha; got != want {
		t.Errorf("root: got %q, want %q", got, want)
	}
}
