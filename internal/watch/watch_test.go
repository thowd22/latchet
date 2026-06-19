package watch

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/thowd22/latchet/internal/globalconfig"
)

const repo = "git@github.com:me/app.git"

func branchEntry() globalconfig.WatchEntry {
	return globalconfig.WatchEntry{URL: repo, Branches: []string{"main"}}
}

func TestBranchFirstSightBaselinesNoFire(t *testing.T) {
	refs := RemoteRefs{Branches: map[string]string{"main": "aaa"}}
	fires, next := decide(branchEntry(), refs, map[string]string{})
	if len(fires) != 0 {
		t.Errorf("first sight should not fire, got %v", fires)
	}
	if next[repo+sep+"refs/heads/main"] != "aaa" {
		t.Errorf("baseline not recorded: %v", next)
	}
}

func TestBranchAdvanceFires(t *testing.T) {
	state := map[string]string{repo + sep + "refs/heads/main": "aaa"}
	refs := RemoteRefs{Branches: map[string]string{"main": "bbb"}}
	fires, next := decide(branchEntry(), refs, state)
	want := []Fire{{repo, "refs/heads/main", "bbb"}}
	if !reflect.DeepEqual(fires, want) {
		t.Errorf("advance: got %v want %v", fires, want)
	}
	if next[repo+sep+"refs/heads/main"] != "bbb" {
		t.Errorf("state not advanced: %v", next)
	}
}

func TestBranchUnchangedNoFire(t *testing.T) {
	state := map[string]string{repo + sep + "refs/heads/main": "aaa"}
	refs := RemoteRefs{Branches: map[string]string{"main": "aaa"}}
	fires, _ := decide(branchEntry(), refs, state)
	if len(fires) != 0 {
		t.Errorf("unchanged should not fire, got %v", fires)
	}
}

func tagEntry() globalconfig.WatchEntry {
	return globalconfig.WatchEntry{URL: repo, Tags: []string{"v*"}}
}

func TestTagPatternBaselineNoFire(t *testing.T) {
	refs := RemoteRefs{Tags: map[string]string{"v1.0.0": "t1", "other": "x"}}
	fires, next := decide(tagEntry(), refs, map[string]string{})
	if len(fires) != 0 {
		t.Errorf("first pattern pass should baseline, not fire: %v", fires)
	}
	if next[repo+sep+"pattern:v*"] != "1" {
		t.Error("pattern marker not set")
	}
	if next[repo+sep+"refs/tags/v1.0.0"] != "t1" {
		t.Error("matching tag not baselined")
	}
	if _, ok := next[repo+sep+"refs/tags/other"]; ok {
		t.Error("non-matching tag should be ignored")
	}
}

func TestNewTagFiresAfterBaseline(t *testing.T) {
	state := map[string]string{
		repo + sep + "pattern:v*":       "1",
		repo + sep + "refs/tags/v1.0.0": "t1",
	}
	refs := RemoteRefs{Tags: map[string]string{"v1.0.0": "t1", "v2.0.0": "t2"}}
	fires, next := decide(tagEntry(), refs, state)
	want := []Fire{{repo, "refs/tags/v2.0.0", "t2"}}
	if !reflect.DeepEqual(fires, want) {
		t.Errorf("new tag: got %v want %v", fires, want)
	}
	if next[repo+sep+"refs/tags/v2.0.0"] != "t2" {
		t.Error("new tag not recorded")
	}
}

func TestTagMoveFires(t *testing.T) {
	state := map[string]string{
		repo + sep + "pattern:v*":       "1",
		repo + sep + "refs/tags/v1.0.0": "t1",
	}
	refs := RemoteRefs{Tags: map[string]string{"v1.0.0": "t1-moved"}}
	fires, _ := decide(tagEntry(), refs, state)
	want := []Fire{{repo, "refs/tags/v1.0.0", "t1-moved"}}
	if !reflect.DeepEqual(fires, want) {
		t.Errorf("tag move: got %v want %v", fires, want)
	}
}

func TestParseLsRemote(t *testing.T) {
	out := "" +
		"aaa\trefs/heads/main\n" +
		"bbb\trefs/heads/dev\n" +
		"ccc\trefs/tags/v1.0.0\n" +
		"ddd\trefs/tags/v1.0.0^{}\n" + // peeled commit overrides annotated tag object
		"eee\trefs/pull/7/head\n" // ignored
	r := parseLsRemote(out)
	if r.Branches["main"] != "aaa" || r.Branches["dev"] != "bbb" {
		t.Errorf("branches wrong: %v", r.Branches)
	}
	if r.Tags["v1.0.0"] != "ddd" {
		t.Errorf("annotated tag should resolve to peeled commit, got %v", r.Tags)
	}
	if len(r.Branches) != 2 || len(r.Tags) != 1 {
		t.Errorf("unexpected ref counts: %v %v", r.Branches, r.Tags)
	}
}

func TestStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if m, err := loadState(p); err != nil || len(m) != 0 {
		t.Fatalf("missing state should be empty: %v %v", m, err)
	}
	want := map[string]string{repo + sep + "refs/heads/main": "aaa"}
	if err := saveState(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(p)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("round trip: got %v (%v) want %v", got, err, want)
	}
}
