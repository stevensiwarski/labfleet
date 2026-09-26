package fleetctl

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"version", "--output", "json"}, nil, &out, &errout); code != 0 {
		t.Fatal(code)
	}
	var got envelope
	if e := json.Unmarshal(out.Bytes(), &got); e != nil || got.Command != "version" || got.ExitCode != 0 {
		t.Fatalf("output=%q error=%v", out.String(), e)
	}
	if errout.Len() != 0 {
		t.Fatalf("stderr=%q", errout.String())
	}
}

func TestBadOutputIsUsageError(t *testing.T) {
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--output", "xml"}, nil, &out, &errout); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errout.String(), "output must be text or json") {
		t.Fatalf("stderr=%q", errout.String())
	}
}

func TestCommandErrorsAreStructured(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"destroy", "--output", "json"}, 4},
		{[]string{"provision", "--output=json"}, 4},
		{[]string{"status", "--apply", "--output=json"}, 2},
		{[]string{"provision", "--plan", "--apply", "--output=json"}, 2},
		{[]string{"destroy", "--plan", "--yes", "--output=json"}, 2},
		{[]string{"provision", "--yes", "--output=json"}, 2},
		{[]string{"unknown", "--output=json"}, 2},
		{[]string{"status", "--unknown", "--output=json"}, 2},
	} {
		var out, errout bytes.Buffer
		got := Run(context.Background(), tc.args, nil, &out, &errout)
		var e envelope
		if got != tc.code || json.Unmarshal(out.Bytes(), &e) != nil || e.ExitCode != tc.code || e.Error == nil {
			t.Fatalf("%v => %d %s", tc.args, got, out.String())
		}
	}
}

func TestVersionNeedsNoLiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Version is deliberately configuration-free and does not start work.
	var out, errout bytes.Buffer
	if code := Run(ctx, []string{"version", "--output=json"}, nil, &out, &errout); code != 0 {
		t.Fatal(code)
	}
}
