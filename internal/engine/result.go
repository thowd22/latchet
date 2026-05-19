package engine

// Status is the final outcome of a job.
type Status string

const (
	// StatusSuccess means every step in the job exited 0.
	StatusSuccess Status = "success"
	// StatusFailed means a step exited non-zero.
	StatusFailed Status = "failed"
	// StatusSkipped means a job the job depends on did not succeed, so the
	// job was never started.
	StatusSkipped Status = "skipped"
)

// JobResult records how one job ended.
type JobResult struct {
	ID     string
	Status Status
	Detail string // failing step, or the reason a job was skipped
}
