package main

import "testing"

func TestDescription(t *testing.T) {
	if got, want := description(), "LabFleet fleet operations CLI; use 'fleetctl help' for commands"; got != want {
		t.Fatalf("description() = %q, want %q", got, want)
	}
}
