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

func TestBuildPopulatesGraph(t *testing.T) {
	// a -> {b, c} -> d
	deps := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	g, err := Build(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Order must respect every edge.
	for _, edge := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}} {
		if indexOf(g.Order, edge[0]) >= indexOf(g.Order, edge[1]) {
			t.Errorf("%s must come before %s in %v", edge[0], edge[1], g.Order)
		}
	}

	// Indegree must reflect each node's number of needs.
	wantIndeg := map[string]int{"a": 0, "b": 1, "c": 1, "d": 2}
	if !reflect.DeepEqual(g.Indegree, wantIndeg) {
		t.Errorf("Indegree = %v, want %v", g.Indegree, wantIndeg)
	}

	// Dependents of a are b and c (order doesn't matter).
	depsOfA := append([]string(nil), g.Dependents["a"]...)
	sortStrings(depsOfA)
	if !reflect.DeepEqual(depsOfA, []string{"b", "c"}) {
		t.Errorf("Dependents[a] = %v, want [b c]", g.Dependents["a"])
	}
}

func TestWavesLinear(t *testing.T) {
	deps := map[string][]string{
		"a": nil, "b": {"a"}, "c": {"b"},
	}
	g, err := Build(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := Waves(g)
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
}

func TestWavesDiamond(t *testing.T) {
	deps := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	g, err := Build(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := Waves(g)
	// b and c are parallel in wave 2.
	want := [][]string{{"a"}, {"b", "c"}, {"d"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
}

func TestWavesFanOutFanIn(t *testing.T) {
	// a -> {b, c, d} -> e
	deps := map[string][]string{
		"a": nil,
		"b": {"a"}, "c": {"a"}, "d": {"a"},
		"e": {"b", "c", "d"},
	}
	g, err := Build(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := Waves(g)
	want := [][]string{{"a"}, {"b", "c", "d"}, {"e"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
}

func TestBuildDoesNotMutateInputDeps(t *testing.T) {
	deps := map[string][]string{
		"a": nil, "b": {"a"},
	}
	_, err := Build(deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 2 || !reflect.DeepEqual(deps["b"], []string{"a"}) {
		t.Fatalf("Build mutated input deps: %v", deps)
	}
}

// sortStrings is a tiny helper used by tests to compare unordered slices.
func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
