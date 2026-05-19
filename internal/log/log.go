// Package log writes latchet's plain-text run output. It is deliberately
// minimal: no color, no JSON, no levels — just job and step markers streamed
// to stdout, plus an end-of-run summary.
package log

import (
	"fmt"
	"io"
	"time"
)

// JobStart prints the header that precedes a job's step output.
func JobStart(w io.Writer, id string) {
	fmt.Fprintf(w, "\n== job: %s ==\n", id)
}

// JobEnd prints a job's outcome. detail may be empty.
func JobEnd(w io.Writer, id, status, detail string) {
	if detail != "" {
		fmt.Fprintf(w, "== job: %s -> %s (%s) ==\n", id, status, detail)
	} else {
		fmt.Fprintf(w, "== job: %s -> %s ==\n", id, status)
	}
}

// JobSkip prints a one-line notice for a job that never ran.
func JobSkip(w io.Writer, id, reason string) {
	fmt.Fprintf(w, "\n== job: %s -> skipped (%s) ==\n", id, reason)
}

// StepStart prints the header that precedes a step's command output.
func StepStart(w io.Writer, name string) {
	fmt.Fprintf(w, "-- step: %s --\n", name)
}

// StepEnd prints a step's outcome and how long it took.
func StepEnd(w io.Writer, name string, ok bool, d time.Duration) {
	status := "ok"
	if !ok {
		status = "FAILED"
	}
	fmt.Fprintf(w, "-- step: %s -> %s (%s) --\n", name, status, d.Round(time.Millisecond))
}

// SummaryHeader prints the heading for the per-job summary table.
func SummaryHeader(w io.Writer) {
	fmt.Fprintln(w, "\n== summary ==")
}

// SummaryLine prints one job's final status in the summary table.
func SummaryLine(w io.Writer, id, status string) {
	fmt.Fprintf(w, "  %-20s %s\n", id, status)
}
