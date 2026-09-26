package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stevensiwarski/labfleet/internal/fleetctl"
	"github.com/stevensiwarski/labfleet/internal/runner"
)

type fakeRunner struct {
	command runner.Command
	result  runner.Result
	err     error
}

func (f *fakeRunner) Run(_ context.Context, c runner.Command) (runner.Result, error) {
	f.command = c
	return f.result, f.err
}

func TestRemoteProbePinsSSHAndParsesDiagnosticExit(t *testing.T) {
	want := Report{Target: "labfleet-worker-01", Status: "unhealthy", ExitCode: 3, Results: []Result{{Name: "disk", Target: "labfleet-worker-01", Status: Fail, Message: "full"}}}
	b, _ := json.Marshal(want)
	f := &fakeRunner{result: runner.Result{Stdout: b, ExitCode: 3}}
	h := fleetctl.DiagnosticHost{VM: fleetctl.VM{Name: "labfleet-worker-01"}, Address: "192.0.2.5", SSHKey: "/secure/key", KnownHosts: "/secure/known_hosts"}
	got := RunRemoteProbe(context.Background(), h, ProbeRequest{Mode: "node", TimeoutSeconds: 10}, "/usr/local/bin/node-doctor", f)
	if got.ExitCode != 3 || len(got.Results) != 1 {
		t.Fatalf("report: %#v", got)
	}
	args := strings.Join(f.command.Args, " ")
	for _, part := range []string{"-F /dev/null", "StrictHostKeyChecking=yes", "GlobalKnownHostsFile=/dev/null", "UserKnownHostsFile=/secure/known_hosts", "HostKeyAlias=labfleet-worker-01", "fleet@192.0.2.5", "sudo -n /usr/local/bin/node-doctor probe"} {
		if !strings.Contains(args, part) {
			t.Errorf("ssh args lack %q: %s", part, args)
		}
	}
	if !bytes.Contains(mustRead(f.command.Stdin), []byte(`"mode":"node"`)) {
		t.Fatal("structured request was not sent over stdin")
	}
}

func mustRead(r interface{ Read([]byte) (int, error) }) []byte {
	var b bytes.Buffer
	_, _ = b.ReadFrom(r)
	return b.Bytes()
}

func TestRemoteProbeRejectsUnsafeExecutableBeforeRunner(t *testing.T) {
	f := &fakeRunner{}
	h := fleetctl.DiagnosticHost{VM: fleetctl.VM{Name: "labfleet-worker-01"}, Address: "192.0.2.5", SSHKey: "key", KnownHosts: "known"}
	for _, p := range []string{"relative", "/tmp/../bin/probe", "/tmp/a;id", "/tmp/a b", "/bin/sh", "/usr/bin/tee", "/tmp/node-doctor"} {
		r := RunRemoteProbe(context.Background(), h, ProbeRequest{}, p, f)
		if r.ExitCode != 3 {
			t.Fatalf("path %q accepted", p)
		}
	}
	if f.command.Name != "" {
		t.Fatal("unsafe path invoked runner")
	}
}

func TestRunProbeCLIRejectsArguments(t *testing.T) {
	var out, errout bytes.Buffer
	if got := runProbeCLI(context.Background(), []string{"/bin/sh"}, strings.NewReader(""), &out, &errout); got != 2 {
		t.Fatalf("exit=%d", got)
	}
}

func TestCanceledRemoteResultCannotReportHealthy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := fleetctl.DiagnosticHost{VM: fleetctl.VM{Name: "labfleet-worker-01"}, Address: "192.0.2.5", SSHKey: "/secure/key", KnownHosts: "/secure/known_hosts"}
	b, _ := json.Marshal(Report{Target: h.VM.Name, Status: "healthy", ExitCode: 0, Results: []Result{{Name: "check", Target: h.VM.Name, Status: Pass}}})
	f := &fakeRunner{result: runner.Result{Stdout: b}}
	got := RunRemoteProbe(ctx, h, ProbeRequest{}, remoteBinaryPath, f)
	if got.ExitCode != 130 || got.Status != "canceled" {
		t.Fatalf("contradictory cancellation: %+v", got)
	}
}

func TestRemoteCancellationWithLiveControllerContext(t *testing.T) {
	h := fleetctl.DiagnosticHost{VM: fleetctl.VM{Name: "labfleet-worker-01"}, Address: "192.0.2.5", SSHKey: "/secure/key", KnownHosts: "/secure/known_hosts"}
	b, _ := json.Marshal(Report{Target: h.VM.Name, Status: "canceled", ExitCode: 130, Results: []Result{{Name: "check", Target: h.VM.Name, Status: Fail, Message: "canceled"}}})
	f := &fakeRunner{result: runner.Result{Stdout: b, ExitCode: 130}}
	got := RunRemoteProbe(context.Background(), h, ProbeRequest{}, remoteBinaryPath, f)
	if got.ExitCode != 130 || got.Status != "canceled" {
		t.Fatalf("remote cancellation lost: %+v", got)
	}
}
