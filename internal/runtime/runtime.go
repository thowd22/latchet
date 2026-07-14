// Package runtime drives a container runtime (docker or podman) by shelling
// out to its CLI. docker and podman share the subcommands latchet needs
// (create/start/exec/rm/image inspect/pull), so one code path serves both.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runtime is a resolved container CLI.
type Runtime struct {
	Bin string // "docker" or "podman"
}

// Detect resolves which runtime to use. LATCHET_RUNTIME overrides everything;
// otherwise docker is preferred, then podman.
func Detect() (*Runtime, error) {
	lookPath := func(name string) (string, bool) {
		p, err := exec.LookPath(name)
		return p, err == nil
	}
	bin, err := detect(os.Getenv("LATCHET_RUNTIME"), lookPath)
	if err != nil {
		return nil, err
	}
	return &Runtime{Bin: bin}, nil
}

// detect is the pure core of Detect, with PATH lookup injected for testing.
func detect(override string, lookPath func(string) (string, bool)) (string, error) {
	if override != "" {
		if _, ok := lookPath(override); !ok {
			return "", fmt.Errorf("LATCHET_RUNTIME=%q: not found in PATH", override)
		}
		return override, nil
	}
	for _, bin := range []string{"docker", "podman"} {
		if _, ok := lookPath(bin); ok {
			return bin, nil
		}
	}
	return "", errors.New("no container runtime found: install docker or podman, or set LATCHET_RUNTIME")
}

// --- pure command builders (binary name excluded; table-tested) ---

// createArgs builds the args to create a long-lived job container. The
// container idles on `sleep infinity` so that each step can `exec` into it
// without paying container startup cost again. The image's own entrypoint is
// overridden: steps always run via exec, and an image whose ENTRYPOINT is a
// tool (e.g. alpine/git) would otherwise swallow the keepalive command and
// exit immediately.
func createArgs(name, image, workspaceHost string) []string {
	return []string{
		"create",
		"--name", name,
		"-w", "/workspace",
		"-v", workspaceHost + ":/workspace",
		"--entrypoint", "sh",
		image,
		"-c", "sleep infinity",
	}
}

func startArgs(name string) []string { return []string{"start", name} }

// execArgs builds the args to run one step inside a job container. `set -e` is
// prepended so any failing line in a multi-line script fails the step.
func execArgs(name string, env []string, script string) []string {
	args := []string{"exec", "-w", "/workspace"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	return append(args, name, "sh", "-c", "set -e\n"+script)
}

func rmArgs(name string) []string       { return []string{"rm", "-f", name} }
func inspectArgs(image string) []string { return []string{"image", "inspect", image} }
func pullArgs(image string) []string    { return []string{"pull", image} }

func digestArgs(image string) []string {
	return []string{"image", "inspect", "--format", "{{index .RepoDigests 0}}", image}
}

// --- execution ---

// ImageExists reports whether the image is already present locally, so the
// caller can skip a redundant pull.
func (r *Runtime) ImageExists(ctx context.Context, image string) bool {
	c := exec.CommandContext(ctx, r.Bin, inspectArgs(image)...)
	c.Stdout, c.Stderr = io.Discard, io.Discard
	return c.Run() == nil
}

// ImageDigest returns the digest-pinned reference of a locally-present image
// (e.g. "docker.io/library/golang@sha256:..."), read from its first
// RepoDigest. Used to record resolvedDependencies in provenance. Returns ""
// with no error when the image has no RepoDigest (e.g. a locally-built image
// never pushed/pulled) so provenance emission degrades gracefully.
func (r *Runtime) ImageDigest(ctx context.Context, image string) (string, error) {
	out, err := exec.CommandContext(ctx, r.Bin, digestArgs(image)...).Output()
	if err != nil {
		return "", fmt.Errorf("inspecting image %s: %w", image, err)
	}
	digest := strings.TrimSpace(string(out))
	if digest == "<no value>" { // template produced nothing
		return "", nil
	}
	return digest, nil
}

// Pull fetches an image, streaming progress to out. On failure the runtime's
// own diagnostic (e.g. "short-name ... did not resolve") is captured and
// folded into the returned error, so the console message is actionable rather
// than a bare "exit status 125".
func (r *Runtime) Pull(ctx context.Context, image string, out io.Writer) error {
	tail := &tailWriter{max: 2048}
	c := exec.CommandContext(ctx, r.Bin, pullArgs(image)...)
	w := io.MultiWriter(out, tail)
	c.Stdout, c.Stderr = w, w
	if err := c.Run(); err != nil {
		return pullError(image, err, tail.String())
	}
	return nil
}

// pullError formats a pull failure, appending the runtime's captured output
// when there is any so the caller sees why the pull failed.
func pullError(image string, err error, captured string) error {
	if captured = strings.TrimSpace(captured); captured != "" {
		return fmt.Errorf("pulling image %s: %w\n%s", image, err, captured)
	}
	return fmt.Errorf("pulling image %s: %w", image, err)
}

// tailWriter retains the last max bytes written to it, used to surface the
// end of a streamed subprocess's output in an error message.
type tailWriter struct {
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return string(w.buf) }

// Create creates and starts the long-lived container for a job.
func (r *Runtime) Create(ctx context.Context, name, image, workspaceHost string) error {
	if out, err := exec.CommandContext(ctx, r.Bin, createArgs(name, image, workspaceHost)...).CombinedOutput(); err != nil {
		return fmt.Errorf("creating container %s: %w\n%s", name, err, out)
	}
	if out, err := exec.CommandContext(ctx, r.Bin, startArgs(name)...).CombinedOutput(); err != nil {
		return fmt.Errorf("starting container %s: %w\n%s", name, err, out)
	}
	return nil
}

// Exec runs one step's script inside the job container, streaming output live.
// A non-zero exit code is returned with a nil error — that is a step failure,
// not an infrastructure failure. A non-nil error means the runtime itself
// could not run the command.
func (r *Runtime) Exec(ctx context.Context, name string, env []string, script string, stdout, stderr io.Writer) (int, error) {
	c := exec.CommandContext(ctx, r.Bin, execArgs(name, env, script)...)
	c.Stdout, c.Stderr = stdout, stderr
	err := c.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, fmt.Errorf("executing step in %s: %w", name, err)
}

// Remove force-deletes a job container. It deliberately does not take a
// context so cleanup still runs after the run's context is cancelled.
func (r *Runtime) Remove(name string) error {
	c := exec.Command(r.Bin, rmArgs(name)...)
	c.Stdout, c.Stderr = io.Discard, io.Discard
	return c.Run()
}
