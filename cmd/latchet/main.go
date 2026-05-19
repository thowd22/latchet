// Command latchet runs a single CI/CD workflow defined in ./latchet.yml.
//
// It takes no arguments: it reads latchet.yml from the current working
// directory, executes every job inside a container, and exits with a status
// code reflecting the outcome (see internal/engine for the codes).
package main

import (
	"fmt"
	"os"

	"github.com/thowd22/latchet/internal/engine"
)

const workflowFile = "latchet.yml"

func main() {
	if _, err := os.Stat(workflowFile); err != nil {
		fmt.Fprintf(os.Stderr, "latchet: cannot read ./%s: %v\n", workflowFile, err)
		os.Exit(engine.ExitConfig)
	}
	os.Exit(engine.Run(workflowFile))
}
