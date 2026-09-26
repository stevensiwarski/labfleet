package fleetctl

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

func TestToolFailureExitCodesAndJSON(t *testing.T) {
	for _, tc := range []struct {
		err  *runner.ExecutionError
		code int
	}{
		{&runner.ExecutionError{Tool: "tofu", ExitCode: 7}, 1},
		{&runner.ExecutionError{Tool: "tofu", ExitCode: -1, TimedOut: true}, 1},
		{&runner.ExecutionError{Tool: "tofu", ExitCode: -1, Canceled: true}, 130},
	} {
		e := runnerFailure("provision", tc.err)
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var decoded envelope
		if json.Unmarshal(b, &decoded) != nil || decoded.ExitCode != tc.code || decoded.Error == nil {
			t.Fatalf("%s", b)
		}
		if tc.err.ExitCode == 7 && (decoded.Error.ToolExitCode == nil || *decoded.Error.ToolExitCode != 7) {
			t.Fatalf("lost subprocess code: %s", b)
		}
	}
}

func TestConfirmationCanBeCanceled(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- confirmDestroyContext(ctx, r, io.Discard, "test") }()
	cancel()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("canceled confirmation accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation ignored cancellation")
	}
}

func TestPlanComparisonIgnoresObjectOrderNotValues(t *testing.T) {
	if !samePlanJSON([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`)) {
		t.Fatal("equivalent show output rejected")
	}
	if samePlanJSON([]byte(`{"a":1}`), []byte(`{"a":2}`)) {
		t.Fatal("changed plan accepted")
	}
}
