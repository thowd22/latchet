// Package envutil merges environment variables across the workflow, job, and
// step levels.
package envutil

import (
	"fmt"
	"sort"
)

// Merge combines environment maps in order of increasing precedence: workflow
// is the base, job overrides it, and step overrides both. The result is a
// sorted slice of "KEY=VALUE" strings, ready to pass as `-e` flags. Sorting
// keeps generated container commands deterministic.
func Merge(levels ...map[string]string) []string {
	merged := make(map[string]string)
	for _, level := range levels {
		for k, v := range level {
			merged[k] = v
		}
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, merged[k]))
	}
	return out
}
