package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCLIHelpAndRejectsUnknownFlags(t *testing.T) {
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"help"}, strings.NewReader(""), &out, &errout); code != 0 || !strings.Contains(out.String(), "node-doctor") {
		t.Fatalf("help: exit=%d output=%q", code, out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"node", "labfleet-worker-01", "--not-a-flag"}, strings.NewReader(""), &out, &errout); code != 2 {
		t.Fatalf("unknown flag exit=%d", code)
	}
}

func TestProbeCLIRejectsTrailingJSON(t *testing.T) {
	var out bytes.Buffer
	code := runProbeCLI(context.Background(), nil, strings.NewReader(`{"target":"labfleet-worker-01","mode":"network"} garbage`), &out, &bytes.Buffer{})
	if code != 2 || !strings.Contains(out.String(), "invalid probe request") {
		t.Fatalf("exit=%d output=%q", code, out.String())
	}
}

func TestCLIUsageErrorsAreJSONAndNodePositionDoesNotPanic(t *testing.T) {
	t.Setenv("FLEETCTL_CONFIG", "")
	for _, args := range [][]string{
		{"node", "labfleet-worker-01", "--output", "json"},
		{"node", "--output", "json", "labfleet-worker-01"},
		{"cluster", "extra", "--output=json"},
		{"unknown", "--output=json"},
		{"node", "labfleet-worker-01", "--concurrency", "99", "--output=json"},
		{"node", "labfleet-worker-01", "--tcp", "bad-endpoint", "--output=json"},
	} {
		var out, errout bytes.Buffer
		code := Run(context.Background(), args, strings.NewReader(""), &out, &errout)
		var report Report
		if code != 2 || json.Unmarshal(out.Bytes(), &report) != nil || report.ExitCode != 2 {
			t.Fatalf("%v: exit%d %s", args, code, out.String())
		}
	}
}
