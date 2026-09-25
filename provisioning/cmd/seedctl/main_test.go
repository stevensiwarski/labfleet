package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresConfig(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("expected missing config error")
	}
}

func TestRunRejectsUnknownConfigFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"unexpected":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"--config", p})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}
