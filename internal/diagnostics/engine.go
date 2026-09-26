package diagnostics

import (
	"context"
	"sync"
	"time"
)

// RunChecks uses a fixed worker pool per invocation. A check that fails to honor
// its context consumes that worker; at most concurrency abandoned check goroutines
// remain for this invocation. Callers must not repeatedly retry stuck checks.
func RunChecks(ctx context.Context, target string, checks []Check, concurrency int, timeout time.Duration) Report {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(checks) {
		concurrency = len(checks)
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	results := make([]Result, len(checks))
	for i, c := range checks {
		results[i] = Result{Name: c.Name, Target: target, Status: Fail, Message: "not run"}
	}
	if concurrency > 0 {
		jobs := make(chan int, len(checks))
		var wg sync.WaitGroup
		wg.Add(concurrency)
		for w := 0; w < concurrency; w++ {
			go func() {
				defer wg.Done()
				for i := range jobs {
					if ctx.Err() != nil {
						results[i].Message = "canceled"
						continue
					}
					started := time.Now()
					c := checks[i]
					checkCtx, cancel := context.WithTimeout(ctx, timeout)
					done := make(chan Result, 1)
					go func() {
						r := Result{Name: c.Name, Target: target, Status: Fail, Message: "check returned no result"}
						if c.Run != nil {
							r = c.Run(checkCtx)
						}
						done <- r
					}()
					select {
					case r := <-done:
						cancel()
						if r.Status != Pass && r.Status != Warn && r.Status != Fail {
							r.Status, r.Message = Fail, "check returned an invalid status"
						}
						r.Name = c.Name
						r.Target = target
						r.DurationMS = time.Since(started).Milliseconds()
						if r.Status != Pass && r.Hint == "" {
							r.Hint = "Inspect this subsystem's configuration and reported thresholds before making changes."
						}
						results[i] = r
					case <-checkCtx.Done():
						cancel()
						results[i].DurationMS = time.Since(started).Milliseconds()
						results[i].Hint = "Check subsystem responsiveness; this check's worker retired to keep concurrency bounded."
						if ctx.Err() != nil {
							results[i].Message = "canceled"
						} else {
							results[i].Message = "check timed out"
						}
						// Do not start another check on this occupied worker.
						return
					}
				}
			}()
		}
		for i := range checks {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		if ctx.Err() != nil {
			for i := range results {
				if results[i].Message == "not run" {
					results[i].Message = "canceled"
				}
			}
		}
	}
	status, code := Aggregate(results)
	if ctx.Err() != nil {
		code = 130
		status = "canceled"
	}
	return Report{Target: target, Status: status, ExitCode: code, Results: results}
}
