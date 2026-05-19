package envutil

import (
	"reflect"
	"testing"
)

func TestMergePrecedence(t *testing.T) {
	workflow := map[string]string{"A": "wf", "SHARED": "wf"}
	job := map[string]string{"B": "job", "SHARED": "job"}
	step := map[string]string{"C": "step", "SHARED": "step"}

	got := Merge(workflow, job, step)
	want := []string{"A=wf", "B=job", "C=step", "SHARED=step"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
}

func TestMergeSortedAndStable(t *testing.T) {
	in := map[string]string{"Z": "1", "A": "2", "M": "3"}
	got := Merge(in)
	want := []string{"A=2", "M=3", "Z=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %v, want %v (must be sorted)", got, want)
	}
}

func TestMergeEmpty(t *testing.T) {
	if got := Merge(); len(got) != 0 {
		t.Fatalf("Merge() = %v, want empty", got)
	}
	if got := Merge(nil, nil); len(got) != 0 {
		t.Fatalf("Merge(nil, nil) = %v, want empty", got)
	}
}
