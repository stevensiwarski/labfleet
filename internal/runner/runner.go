// Package runner provides bounded, shell-free subprocess execution.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	maxOutput = 16 << 20
	waitDelay = 2 * time.Second
)

// Command describes a single executable invocation. A nil Env inherits the
// current process environment; a non-nil Env is passed verbatim to the child.
type Command struct {
	Name    string
	Args    []string
	Dir     string
	Env     []string
	Stdin   io.Reader
	Timeout time.Duration
}

// Result contains bounded stdout and stderr captured from the process.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	TimedOut bool
	Canceled bool
}

// Runner executes commands without invoking a shell.
type Runner interface {
	Run(context.Context, Command) (Result, error)
}

// Exec implements Runner with an OS subprocess.
type Exec struct{}

// ExecutionError is a sanitized process failure; it never includes arguments,
// environment values, or captured output.
type ExecutionError struct {
	Tool        string
	ExitCode    int
	TimedOut    bool
	Canceled    bool
	OutputLimit bool
}

func (e *ExecutionError) Error() string {
	tool := filepath.Base(e.Tool)
	if tool == "" || tool == "." || tool == string(filepath.Separator) {
		tool = "command"
	}
	switch {
	case e.TimedOut:
		return fmt.Sprintf("%s timed out", tool)
	case e.Canceled:
		return fmt.Sprintf("%s canceled", tool)
	case e.OutputLimit:
		return fmt.Sprintf("%s exceeded output limit", tool)
	default:
		return fmt.Sprintf("%s exited with status %d", tool, e.ExitCode)
	}
}

type cappedBuffer struct {
	data     []byte
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxOutput - len(b.data)
	if remaining > 0 {
		keep := len(p)
		if keep > remaining {
			keep = remaining
		}
		b.data = append(b.data, p[:keep]...)
	}
	if n > remaining {
		b.overflow = true
	}
	// Consume all output so child processes cannot deadlock on a full pipe.
	return n, nil
}

// Run executes the requested binary directly, with a required positive timeout.
func (Exec) Run(ctx context.Context, c Command) (Result, error) {
	result := Result{ExitCode: -1}
	if ctx == nil {
		return result, errors.New("runner: nil context")
	}
	if strings.TrimSpace(c.Name) == "" {
		return result, errors.New("runner: command name is required")
	}
	if c.Timeout <= 0 {
		return result, errors.New("runner: positive timeout is required")
	}
	if err := ctx.Err(); err != nil {
		result.TimedOut = errors.Is(err, context.DeadlineExceeded)
		result.Canceled = !result.TimedOut
		return result, &ExecutionError{Tool: c.Name, TimedOut: result.TimedOut, Canceled: result.Canceled}
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = c.Dir, c.Env, c.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = waitDelay
	stdout, stderr := &cappedBuffer{}, &cappedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	result.Stdout, result.Stderr = []byte{}, []byte{}
	err := cmd.Run()
	// A child may outlive the direct process while holding inherited pipes open.
	// WaitDelay bounds cmd.Run, but does not terminate those descendants, so
	// clean up only the process group created for this invocation on every exit.
	if cmd.Process != nil {
		if killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) && err == nil {
			err = killErr
		}
	}
	result.Stdout, result.Stderr = stdout.data, stderr.data
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		result.Canceled = !result.TimedOut
		return result, &ExecutionError{Tool: c.Name, ExitCode: result.ExitCode, TimedOut: result.TimedOut, Canceled: result.Canceled}
	}
	if stdout.overflow || stderr.overflow {
		return result, &ExecutionError{Tool: c.Name, ExitCode: result.ExitCode, OutputLimit: true}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, &ExecutionError{Tool: c.Name, ExitCode: result.ExitCode}
		}
		// Avoid exposing OS errors that can contain paths or caller-provided data.
		return result, fmt.Errorf("%s could not be executed", filepath.Base(c.Name))
	}
	return result, nil
}

var _ Runner = Exec{}
