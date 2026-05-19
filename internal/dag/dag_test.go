package dag

import (
	"errors"
	"reflect"
	"testing"
)

// indexOf returns the position of v in order, or -1.
func indexOf(order []string, v string) int {
	for i, s := range order {
		if s == v {
			return i
		}
	}
	return -1
}

func TestSortLinearChain(t *testing.T) {
	deps := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"b"},
	}
	order, err := Sort(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestSortDiamond(t *testing.T) {
	// a -> {b, c} -> d
	deps := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	order, err := Sort(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, edge := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}} {
		if indexOf(order, edge[0]) >= indexOf(order, edge[1]) {
			t.Errorf("%s must come before %s in %v", edge[0], edge[1], order)
		}
	}
}

func TestSortDeterministic(t *testing.T) {
	deps := map[string][]string{
		"a": nil, "b": nil, "c": nil, "d": {"a", "b", "c"},
	}
	first, err := Sort(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := Sort(deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("non-deterministic order: %v vs %v", got, first)
		}
	}
	// Ties break alphabetically.
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("order = %v, want %v", first, want)
	}
}

func TestSortCycle(t *testing.T) {
	deps := map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"b"},
	}
	_, err := Sort(deps)
	if err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CycleError, got %T", err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(ce.Nodes, want) {
		t.Fatalf("cycle nodes = %v, want %v", ce.Nodes, want)
	}
}
