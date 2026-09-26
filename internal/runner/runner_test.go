package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunnerHelper(t *testing.T) {
	if os.Getenv("RUNNER_HELPER") != "1" {
		return
	}
	switch os.Getenv("RUNNER_MODE") {
	case "success":
		fmt.Fprint(os.Stdout, "out")
		fmt.Fprint(os.Stderr, "err")
	case "exit":
		os.Exit(7)
	case "stderr-values":
		fmt.Fprint(os.Stderr, "stderr-secret ", os.Getenv("PRIVATE_VALUE"))
		os.Exit(7)
	case "sleep":
		time.Sleep(30 * time.Second)
	case "overflow":
		_, _ = os.Stdout.Write(make([]byte, maxOutput+1))
	case "grandchild":
		cmd := exec.Command(os.Args[0], "-test.run=TestRunnerHelper")
		cmd.Env = append(os.Environ(), "RUNNER_HELPER=1", "RUNNER_MODE=sleep")
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, cmd.Process.Pid)
		time.Sleep(30 * time.Second)
	case "hold":
		time.Sleep(30 * time.Second)
	case "exit-parent":
		cmd := exec.Command(os.Args[0], "-test.run=TestRunnerHelper")
		cmd.Env = append(os.Environ(), "RUNNER_HELPER=1", "RUNNER_MODE=hold")
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, cmd.Process.Pid)
		os.Exit(0)
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string, timeout time.Duration) (Result, error) {
	t.Helper()
	return (Exec{}).Run(context.Background(), Command{
		Name: os.Args[0], Args: []string{"-test.run=TestRunnerHelper"},
		Env: []string{"RUNNER_HELPER=1", "RUNNER_MODE=" + mode, "PRIVATE_VALUE=do-not-leak"}, Timeout: timeout,
	})
}

func TestRunSuccessAndExit(t *testing.T) {
	r, err := helper(t, "success", 10*time.Second)
	if err != nil || string(r.Stdout) != "out" || string(r.Stderr) != "err" || r.ExitCode != 0 {
		t.Fatalf("success result = %#v, %v", r, err)
	}
	r, err = helper(t, "exit", 10*time.Second)
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || executionErr.ExitCode != 7 || r.ExitCode != 7 {
		t.Fatalf("exit result = %#v, %v", r, err)
	}
	if strings.Contains(err.Error(), "PRIVATE_VALUE") || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("error leaked caller data: %v", err)
	}
}

func TestRunErrorDoesNotExposeCallerValues(t *testing.T) {
	r, err := (Exec{}).Run(context.Background(), Command{
		Name: os.Args[0], Args: []string{"-test.run=TestRunnerHelper", "arg-secret"},
		Env: []string{"RUNNER_HELPER=1", "RUNNER_MODE=stderr-values", "PRIVATE_VALUE=env-secret"}, Timeout: 10 * time.Second,
	})
	if err == nil || r.ExitCode != 7 {
		t.Fatalf("exit result = %#v, %v", r, err)
	}
	for _, secret := range []string{"arg-secret", "env-secret", "stderr-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestRunTimeout(t *testing.T) {
	r, err := helper(t, "sleep", 30*time.Millisecond)
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || !r.TimedOut || r.Canceled {
		t.Fatalf("timeout result = %#v, %v", r, err)
	}
}

func TestRunCancellationKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		r   Result
		err error
	}, 1)
	go func() {
		r, err := (Exec{}).Run(ctx, Command{
			Name: os.Args[0], Args: []string{"-test.run=TestRunnerHelper"},
			Env: []string{"RUNNER_HELPER=1", "RUNNER_MODE=grandchild"}, Timeout: 5 * time.Second,
		})
		done <- struct {
			r   Result
			err error
		}{r, err}
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case got := <-done:
		if got.err == nil || !got.r.Canceled {
			t.Fatalf("cancellation result = %#v, %v", got.r, got.err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("process group cancellation did not return")
	}
}

func TestRunCleansProcessGroupAfterParentExits(t *testing.T) {
	r, err := helper(t, "exit-parent", 30*time.Second)
	pid, parseErr := strconv.Atoi(string(r.Stdout))
	if parseErr != nil {
		t.Fatalf("child PID %q: %v", r.Stdout, parseErr)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if processGone(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived Run return (run error: %v)", pid, err)
}

func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	// A killed orphan can remain briefly as a zombie until reaped by init.
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	return errors.Is(err, os.ErrNotExist) || (err == nil && strings.Contains(string(status), "State:\tZ"))
}

func TestRunRejectsMissingTimeout(t *testing.T) {
	_, err := (Exec{}).Run(context.Background(), Command{Name: "tool"})
	if err == nil || !strings.Contains(err.Error(), "positive timeout") {
		t.Fatalf("missing timeout error = %v", err)
	}
}

func TestRunPreCanceledContextFlagsDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	r, err := (Exec{}).Run(ctx, Command{Name: "tool", Timeout: time.Second})
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || !r.TimedOut || r.Canceled {
		t.Fatalf("expired context result = %#v, %v", r, err)
	}
}

func TestRunOutputLimit(t *testing.T) {
	r, err := helper(t, "overflow", 10*time.Second)
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || !executionErr.OutputLimit || len(r.Stdout) != maxOutput {
		t.Fatalf("overflow result = stdout %d, err %v", len(r.Stdout), err)
	}
}
