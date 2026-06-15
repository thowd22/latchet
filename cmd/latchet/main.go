// Command latchet runs a single CI/CD workflow.
//
// With no arguments, it reads ./latchet.yml from the current working
// directory. See `latchet -help` for the available flags. Exit codes are
// defined by internal/engine.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/thowd22/latchet/internal/engine"
	"github.com/thowd22/latchet/internal/version"
)

const defaultWorkflowFile = "latchet.yml"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point: it parses argv (without the program name),
// dispatches to the right action, and returns a process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	// Subcommands are bare words (no leading dash); flags start with "-".
	if len(args) > 0 && args[0] == "verify" {
		return runVerify(args[1:], stdout, stderr)
	}

	fs := flag.NewFlagSet("latchet", flag.ContinueOnError)
	fs.SetOutput(stderr)

	defaultParallel := runtime.NumCPU()
	var (
		file         = fs.String("file", defaultWorkflowFile, "workflow file to run")
		validateOnly = fs.Bool("validate-only", false, "load and validate the workflow, then exit")
		dryRun       = fs.Bool("dry-run", false, "print the execution plan and exit; no containers spawned")
		maxParallel  = fs.Int("max-parallel", defaultParallel, "maximum jobs to run concurrently; 1 streams output live like v1")
		showVersion  = fs.Bool("version", false, "print version and exit")
		showHelp     = fs.Bool("help", false, "print usage and exit")
		showHelpH    = fs.Bool("h", false, "print usage and exit (alias for -help)")
	)
	fs.Usage = func() { printUsage(stderr) }

	if err := fs.Parse(args); err != nil {
		// flag's ContinueOnError already wrote the error and usage.
		return engine.ExitConfig
	}
	if *showHelp || *showHelpH {
		printUsage(stdout)
		return engine.ExitSuccess
	}
	if *showVersion {
		fmt.Fprintf(stdout, "latchet %s (%s) %s %s/%s\n",
			version.Version, version.Commit,
			runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return engine.ExitSuccess
	}

	if *maxParallel < 1 {
		fmt.Fprintf(stderr, "latchet: -max-parallel must be >= 1 (got %d)\n", *maxParallel)
		return engine.ExitConfig
	}

	if _, err := os.Stat(*file); err != nil {
		fmt.Fprintf(stderr, "latchet: cannot read %s: %v\n", *file, err)
		return engine.ExitConfig
	}

	opts := engine.Options{
		File:        *file,
		DryRun:      *dryRun,
		MaxParallel: *maxParallel,
		Stdout:      stdout,
		Stderr:      stderr,
	}
	// -validate-only wins if both are set (it's the stricter subset).
	if *validateOnly {
		return engine.Validate(opts)
	}
	if *dryRun {
		return engine.DryRun(opts)
	}
	return engine.Run(opts)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `latchet — minimal container-based workflow engine

Usage:
  latchet [flags]

Flags:
  -file PATH       workflow file to run (default ./latchet.yml)
  -validate-only   load and validate the workflow, then exit
  -dry-run         print the execution plan and exit; no containers spawned
  -max-parallel N  maximum jobs to run concurrently (default: NumCPU; 1 streams output live)
  -version         print version and exit
  -help, -h        print this help and exit

Subcommands:
  verify [flags] PROVENANCE   re-derive a run from its provenance.json and
                              compare; see 'latchet verify -h'

With no flags, latchet reads ./latchet.yml from the current directory.`)
}

// runVerify handles `latchet verify [flags] <provenance.json>`.
func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("latchet verify", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		strict      = fs.Bool("strict", false, "require every subject to match bit-for-bit")
		explain     = fs.Bool("explain", false, "print per-subject mismatch detail")
		file        = fs.String("file", "", "workflow file to re-run (default: path recorded in the manifest)")
		maxParallel = fs.Int("max-parallel", runtime.NumCPU(), "maximum jobs to run concurrently")
	)
	fs.Usage = func() { printVerifyUsage(stderr) }

	if err := fs.Parse(args); err != nil {
		return engine.ExitConfig
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "latchet verify: expected exactly one provenance file")
		printVerifyUsage(stderr)
		return engine.ExitConfig
	}

	return engine.Verify(engine.VerifyOptions{
		ManifestPath: rest[0],
		File:         *file,
		Strict:       *strict,
		Explain:      *explain,
		MaxParallel:  *maxParallel,
		Stdout:       stdout,
		Stderr:       stderr,
	})
}

func printVerifyUsage(w io.Writer) {
	fmt.Fprintln(w, `latchet verify — re-derive a build from its provenance and compare

Usage:
  latchet verify [flags] PROVENANCE

Re-runs the workflow recorded in PROVENANCE (a provenance.json) in a fresh
workspace, pinning each image to the digest it resolved to originally, then
re-hashes the produced artifacts and compares them to the manifest.

Flags:
  -strict          require every subject to match bit-for-bit
  -explain         print per-subject mismatch detail
  -file PATH       workflow to re-run (default: path recorded in the manifest)
  -max-parallel N  maximum jobs to run concurrently (default: NumCPU)

Exit codes: 0 verified · 1 verification failed · 2 bad manifest/workflow ·
3 runtime error. A verification report is written to the run's log directory.`)
}
