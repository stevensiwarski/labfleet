// Package diagnostics provides bounded, read-only host and network diagnostics.
package diagnostics

import "context"

type Status string

const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"
)

type Result struct {
	Name       string         `json:"check"`
	Status     Status         `json:"status"`
	Message    string         `json:"message"`
	Target     string         `json:"target"`
	DurationMS int64          `json:"duration_ms"`
	Details    map[string]any `json:"details,omitempty"`
	Hint       string         `json:"hint,omitempty"`
}

type Report struct {
	Target   string   `json:"target"`
	Status   string   `json:"status"`
	ExitCode int      `json:"exit_code"`
	Results  []Result `json:"results"`
}

type Check struct {
	Name string
	Run  func(context.Context) Result
}

func Aggregate(results []Result) (string, int) {
	if len(results) == 0 {
		return "unhealthy", 3
	}
	status, code := "healthy", 0
	for _, r := range results {
		switch r.Status {
		case Fail:
			return "unhealthy", 3
		case Warn:
			status, code = "degraded", 1
		case Pass:
		default:
			return "unhealthy", 3
		}
	}
	return status, code
}
