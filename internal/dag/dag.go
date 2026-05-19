// Package dag provides topological ordering over a dependency graph.
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

// Sort orders nodes so that every node appears after all the nodes it depends
// on. deps maps each node to the nodes it depends on; every dependency named
// must itself be a key of deps.
//
// The result is deterministic: whenever several nodes are ready at once, the
// alphabetically smallest is emitted first. A cycle yields a *CycleError
// naming the nodes that could not be ordered.
func Sort(deps map[string][]string) ([]string, error) {
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
	return order, nil
}
