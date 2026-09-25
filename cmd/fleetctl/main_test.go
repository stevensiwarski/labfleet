package main

import "testing"

func TestDescription(t *testing.T) {
	if got, want := description(), "LabFleet command-line tool (under development)"; got != want {
		t.Fatalf("description() = %q, want %q", got, want)
	}
}
