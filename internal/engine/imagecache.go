package engine

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/thowd22/latchet/internal/runtime"
)

// imageCache deduplicates concurrent image pulls. The first job that asks
// for an image performs the pull and any subsequent concurrent jobs that
// need the same image block on the same sync.Once, sharing the pull's
// outcome — so two parallel jobs sharing alpine:3.19 cause exactly one
// pull.
type imageCache struct {
	mu   sync.Mutex
	once map[string]*sync.Once
	err  map[string]error
}

func newImageCache() *imageCache {
	return &imageCache{
		once: map[string]*sync.Once{},
		err:  map[string]error{},
	}
}

// Ensure makes sure image is locally available, performing a pull if needed.
// out receives pull progress, but only the first caller for a given image
// gets to write to it — subsequent callers' pull is a no-op (the result is
// already cached).
func (c *imageCache) Ensure(ctx context.Context, rt *runtime.Runtime, image string, out io.Writer) error {
	c.mu.Lock()
	o, ok := c.once[image]
	if !ok {
		o = &sync.Once{}
		c.once[image] = o
	}
	c.mu.Unlock()

	o.Do(func() {
		if rt.ImageExists(ctx, image) {
			return
		}
		fmt.Fprintf(out, "pulling image %s ...\n", image)
		if err := rt.Pull(ctx, image, out); err != nil {
			c.mu.Lock()
			c.err[image] = err
			c.mu.Unlock()
		}
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err[image]
}
