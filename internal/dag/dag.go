// Package dag provides topological ordering and parallel-wave grouping over
// a dependency graph.
//
// It is intentionally generic — it knows nothing about jobs or workflows — so
// it carries no dependency on the config package and cannot form an import
// cycle with it.
package dag

import (
	"fmt"
	"sort"
	"strings"
)

// CycleError reports a dependency cycle and the nodes that participate in it.
type CycleError struct {
	Nodes []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("dependency cycle among jobs: %s", strings.Join(e.Nodes, ", "))
}

// Graph is the analyzed form of a dependency map.
//
//   - Order is a flat topological order (deps before dependents).
//   - Needs maps each node to the nodes it depends on — a defensive copy of
//     the input deps, never mutated by this package.
//   - Dependents maps each node to the nodes that depend on it (the reverse
//     of Needs).
//   - Indegree is the initial dependency count per node, unmodified by Build.
type Graph struct {
	Order      []string
	Needs      map[string][]string
	Dependents map[string][]string
	Indegree   map[string]int
}

// Build analyzes deps and returns a Graph. deps maps each node to the nodes
// it depends on; every dependency named must itself be a key of deps. Ties in
// the topological order are broken alphabetically for determinism. A cycle
// yields a *CycleError naming the nodes that could not be ordered.
func Build(deps map[string][]string) (*Graph, error) {
	indeg := make(map[string]int, len(deps))
	for node := range deps {
		indeg[node] = 0
	}
	dependents := make(map[string][]string, len(deps))
	for node, ds := range deps {
		for _, d := range ds {
			indeg[node]++
			dependents[d] = append(dependents[d], node)
		}
	}

	// Snapshot initial indegrees before Kahn's walk mutates the working copy,
	// so Graph.Indegree is meaningful to callers (e.g. Waves).
	initial := make(map[string]int, len(indeg))
	for k, v := range indeg {
		initial[k] = v
	}

	var ready []string
	for node, n := range indeg {
		if n == 0 {
			ready = append(ready, node)
		}
	}

	order := make([]string, 0, len(deps))
	for len(ready) > 0 {
		sort.Strings(ready)
		node := ready[0]
		ready = ready[1:]
		order = append(order, node)
		for _, dep := range dependents[node] {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}

	if len(order) < len(deps) {
		var stuck []string
		for node, n := range indeg {
			if n > 0 {
				stuck = append(stuck, node)
			}
		}
		sort.Strings(stuck)
		return nil, &CycleError{Nodes: stuck}
	}

	// Defensive copy of the input deps so callers can't accidentally mutate
	// the graph's notion of needs after the fact.
	needs := make(map[string][]string, len(deps))
	for k, v := range deps {
		if len(v) == 0 {
			needs[k] = nil
		} else {
			needs[k] = append([]string(nil), v...)
		}
	}

	return &Graph{
		Order:      order,
		Needs:      needs,
		Dependents: dependents,
		Indegree:   initial,
	}, nil
}

// Sort returns just the flat topological order. It is a thin wrapper around
// Build retained for callers that don't need the full graph.
func Sort(deps map[string][]string) ([]string, error) {
	g, err := Build(deps)
	if err != nil {
		return nil, err
	}
	return g.Order, nil
}

// Waves groups the graph's nodes into parallel execution waves. The first
// wave contains every node with no dependencies; each subsequent wave
// contains the nodes whose dependencies all lie in earlier waves. Within a
// wave, nodes are sorted alphabetically for determinism.
//
// Useful for the dry-run plan printer and for visualizing what would run in
// parallel under unlimited concurrency.
func Waves(g *Graph) [][]string {
	indeg := make(map[string]int, len(g.Indegree))
	for k, v := range g.Indegree {
		indeg[k] = v
	}

	var waves [][]string
	for len(indeg) > 0 {
		var wave []string
		for node, n := range indeg {
			if n == 0 {
				wave = append(wave, node)
			}
		}
		if len(wave) == 0 {
			// Shouldn't happen for a Graph returned by Build (acyclic), but
			// guard against misuse.
			break
		}
		sort.Strings(wave)
		waves = append(waves, wave)
		for _, node := range wave {
			delete(indeg, node)
			for _, dep := range g.Dependents[node] {
				indeg[dep]--
			}
		}
	}
	return waves
}
