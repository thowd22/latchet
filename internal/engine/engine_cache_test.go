package engine

import (
	"testing"

	"github.com/thowd22/latchet/internal/builtinenv"
	"github.com/thowd22/latchet/internal/config"
)

func TestJobBuiltinsCacheVar(t *testing.T) {
	git := builtinenv.Git{}
	cases := []struct {
		name string
		wf   *config.Workflow
		job  *config.Job
		want bool
	}{
		{"no cache", &config.Workflow{}, &config.Job{ID: "a"}, false},
		{"job cache", &config.Workflow{}, &config.Job{ID: "a", Cache: true}, true},
		{"workflow cache", &config.Workflow{Cache: true}, &config.Job{ID: "a"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := jobBuiltins("run1", tc.job, tc.wf, git)
			got, ok := m[builtinenv.CacheVar]
			if tc.want && (!ok || got != "/cache") {
				t.Errorf("LATCHET_CACHE = %q (present=%v), want /cache", got, ok)
			}
			if !tc.want && ok {
				t.Errorf("LATCHET_CACHE unexpectedly present: %q", got)
			}
		})
	}
}
