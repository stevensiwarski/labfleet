package main

import "testing"

func TestStatus(t *testing.T) {
	if got, want := status(), "node-doctor: diagnostics ready"; got != want {
		t.Fatalf("status() = %q, want %q", got, want)
	}
}
