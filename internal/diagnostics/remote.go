package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/stevensiwarski/labfleet/internal/fleetctl"
	"github.com/stevensiwarski/labfleet/internal/runner"
)

const remoteBinaryPath = "/usr/local/bin/node-doctor"

func validRemoteBinary(p string) bool {
	return p == remoteBinaryPath
}

// RunRemoteProbe submits a structured, non-shell command to an attested LabFleet node.
func RunRemoteProbe(ctx context.Context, host fleetctl.DiagnosticHost, req ProbeRequest, binary string, r runner.Runner) Report {
	if r == nil {
		r = runner.Exec{}
	}
	if !validRemoteBinary(binary) || net.ParseIP(host.Address) == nil || host.VM.Name == "" || host.SSHKey == "" || host.KnownHosts == "" {
		return remoteError(req.Target, "invalid remote probe destination or binary")
	}
	req.Target = host.VM.Name
	if req.APIServer == "" {
		req.APIServer = host.APIServer
	}
	body, err := json.Marshal(req)
	if err != nil {
		return remoteError(req.Target, "could not encode remote probe request")
	}
	timeout := time.Duration(req.TimeoutSeconds*30+10) * time.Second
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	if timeout > 600*time.Second {
		timeout = 600 * time.Second
	}
	args := []string{"-F", "/dev/null", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes", "-o", "GlobalKnownHostsFile=/dev/null", "-o", "UserKnownHostsFile=" + host.KnownHosts, "-o", "HostKeyAlias=" + host.VM.Name, "-o", "IdentitiesOnly=yes", "-i", host.SSHKey, "fleet@" + host.Address, "sudo", "-n", binary, "probe"}
	out, runErr := r.Run(ctx, runner.Command{Name: "ssh", Args: args, Stdin: bytes.NewReader(body), Timeout: timeout})
	if runErr != nil && len(out.Stdout) == 0 {
		return remoteError(req.Target, "remote probe transport failed")
	}
	var report Report
	dec := json.NewDecoder(bytes.NewReader(out.Stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		return remoteError(req.Target, "remote probe returned invalid data")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return remoteError(req.Target, "remote probe returned invalid data")
	}
	if report.Target != req.Target || len(report.Results) == 0 || len(report.Results) > 128 || report.ExitCode != out.ExitCode || (report.ExitCode != 0 && report.ExitCode != 1 && report.ExitCode != 2 && report.ExitCode != 3 && report.ExitCode != 4 && report.ExitCode != 130) {
		return remoteError(req.Target, "remote probe returned invalid data")
	}
	for _, result := range report.Results {
		if result.Target != req.Target || result.Name == "" || (result.Status != Pass && result.Status != Warn && result.Status != Fail) {
			return remoteError(req.Target, "remote probe returned invalid data")
		}
	}
	status, code := Aggregate(report.Results)
	if report.ExitCode == 2 || report.ExitCode == 4 || report.ExitCode == 130 {
		code = report.ExitCode
	}
	report.Status, report.ExitCode = status, code
	if ctx.Err() != nil || report.ExitCode == 130 {
		report.ExitCode = 130
		report.Status = "canceled"
	}
	return report
}

func remoteError(target, message string) Report {
	return Report{Target: target, Status: "unhealthy", ExitCode: 3, Results: []Result{{Name: "remote.transport", Status: Fail, Target: target, Message: message, Hint: fmt.Sprintf("%s", "verify SSH connectivity and the node-doctor installation")}}}
}
