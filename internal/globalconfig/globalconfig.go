// Package globalconfig loads the machine-wide latchet-ci.yml — user defaults
// (preferred runtime, workspace root, log dir, default env, job concurrency)
// and the list of repositories watched by `latchet watch`. It is distinct from
// the per-project workflow latchet.yml and is entirely optional: with no file,
// latchet behaves exactly as before.
//
// Precedence (highest wins): CLI flags > environment variables > global config
// > built-in defaults. The operational settings are applied by filling unset
// LATCHET_* env vars, so the packages that already read those vars need no
// changes and a real env var always wins.
package globalconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thowd22/latchet/internal/config"
	"gopkg.in/yaml.v3"
)

// FileName is the global config's basename.
const FileName = "latchet-ci.yml"

// Config is the parsed latchet-ci.yml.
type Config struct {
	Runtime       string            `yaml:"runtime"`
	WorkspaceRoot string            `yaml:"workspace_root"`
	LogDir        string            `yaml:"log_dir"`
	MaxParallel   int               `yaml:"max_parallel"`
	Location      string            `yaml:"location"` // machine identity injected as LATCHET_LOCATION (default "local")
	Env           map[string]string `yaml:"env"`
	Watch         []WatchEntry      `yaml:"watch"`
	// Functions are machine-wide reusable step sequences, callable as helpers in
	// any job's `call:`. A workflow's own `functions:` shadow these by name.
	Functions map[string]*config.Function `yaml:"functions"`

	Path string `yaml:"-"` // file it was loaded from; "" when no config exists
}

// WatchEntry is one repository watched by `latchet watch`.
type WatchEntry struct {
	URL      string   `yaml:"url"`
	Branches []string `yaml:"branches"`
	Tags     []string `yaml:"tags"` // glob patterns, e.g. "v*"
}

// Load finds and parses the global config. The search order is:
//
//  1. $LATCHET_CONFIG (explicit path; an error if it is set but unreadable)
//  2. $XDG_CONFIG_HOME/latchet/latchet-ci.yml
//  3. ~/.config/latchet/latchet-ci.yml
//
// When no file exists, Load returns an empty Config and a nil error.
func Load() (*Config, error) {
	if p := os.Getenv("LATCHET_CONFIG"); p != "" {
		return loadFile(p)
	}
	for _, p := range searchPaths() {
		if fileExists(p) {
			return loadFile(p)
		}
	}
	return &Config{}, nil
}

func searchPaths() []string {
	var paths []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		paths = append(paths, filepath.Join(x, "latchet", FileName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "latchet", FileName))
	}
	return paths
}

func loadFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // reject unknown keys, like internal/config

	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	c.Path = path
	return &c, nil
}

// Validate checks the config and returns every problem found at once.
func (c *Config) Validate() error {
	var errs []string
	if c.Runtime != "" && c.Runtime != "docker" && c.Runtime != "podman" {
		errs = append(errs, fmt.Sprintf("runtime: must be \"docker\" or \"podman\", got %q", c.Runtime))
	}
	if c.MaxParallel < 0 {
		errs = append(errs, fmt.Sprintf("max_parallel: must be >= 0, got %d", c.MaxParallel))
	}
	for i, w := range c.Watch {
		if strings.TrimSpace(w.URL) == "" {
			errs = append(errs, fmt.Sprintf("watch[%d]: missing url", i))
			continue
		}
		if len(w.Branches) == 0 && len(w.Tags) == 0 {
			errs = append(errs, fmt.Sprintf("watch[%d] (%s): needs at least one branch or tag pattern", i, w.URL))
		}
	}
	if len(errs) > 0 {
		where := c.Path
		if where == "" {
			where = FileName
		}
		return fmt.Errorf("invalid %s:\n  - %s", where, strings.Join(errs, "\n  - "))
	}
	return nil
}

// ApplyEnvDefaults fills the LATCHET_* operational env vars from the config
// when they are not already set, so a real environment variable (and there are
// no CLI flags for these) always takes precedence.
func (c *Config) ApplyEnvDefaults() {
	setIfUnset("LATCHET_RUNTIME", c.Runtime)
	setIfUnset("LATCHET_WORKSPACE_ROOT", c.WorkspaceRoot)
	setIfUnset("LATCHET_LOG_DIR", c.LogDir)
	setIfUnset("LATCHET_LOCATION", c.Location)
}

func setIfUnset(key, val string) {
	if val != "" && os.Getenv(key) == "" {
		_ = os.Setenv(key, val)
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
