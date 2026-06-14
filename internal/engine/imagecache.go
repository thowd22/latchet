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
	mu     sync.Mutex
	once   map[string]*sync.Once
	err    map[string]error
	digest map[string]string // image ref as written -> resolved @sha256 digest
}

func newImageCache() *imageCache {
	return &imageCache{
		once:   map[string]*sync.Once{},
		err:    map[string]error{},
		digest: map[string]string{},
	}
}

// ResolvedDigests returns a copy of the image-ref -> resolved-digest map
// captured during Ensure, for recording as provenance resolvedDependencies.
func (c *imageCache) ResolvedDigests() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.digest))
	for k, v := range c.digest {
		out[k] = v
	}
	return out
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
		if !rt.ImageExists(ctx, image) {
			fmt.Fprintf(out, "pulling image %s ...\n", image)
			if err := rt.Pull(ctx, image, out); err != nil {
				c.mu.Lock()
				c.err[image] = err
				c.mu.Unlock()
				return
			}
		}
		// Image is present; record its resolved digest for provenance.
		// Best-effort: a missing RepoDigest leaves the entry unset.
		if d, err := rt.ImageDigest(ctx, image); err == nil && d != "" {
			c.mu.Lock()
			c.digest[image] = d
			c.mu.Unlock()
		}
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err[image]
}
