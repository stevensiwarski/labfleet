package main

import "testing"

func TestStatus(t *testing.T) {
	if got, want := status(), "node-doctor: diagnostics not yet implemented"; got != want {
		t.Fatalf("status() = %q, want %q", got, want)
	}
}
