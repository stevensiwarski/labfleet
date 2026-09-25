package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stevensiwarski/labfleet/provisioning/internal/pxe"
)

func TestServeFailsBeforeStartingOnUnidentifiedHost(t *testing.T) {
	dir := t.TempDir()
	// There is deliberately no real dnsmasq, artifact cache, ownership marker,
	// or socket. Host identification must fail without starting any process.
	hostname := "labfleet-not-this-host"
	if actual, _ := os.Hostname(); actual == hostname {
		hostname = "labfleet-another-host"
	}
	c := pxe.Config{
		ServiceHostname: hostname, TargetHostname: "labfleet-test-node",
		Interface: "prov0", ExpectedMAC: "02:00:00:00:00:01", TargetMAC: "02:00:00:00:00:02",
		Address: "192.0.2.1/24", ServiceIP: "192.0.2.1", TargetIP: "192.0.2.2", Subnet: "192.0.2.0/24",
		Username: "fleet", DiskSerial: "labfleet-test", HTTPPort: 8080,
		SSHKeyPath: filepath.Join(dir, "missing.pub"), Artifacts: filepath.Join(dir, "missing-cache"),
		Output: filepath.Join(dir, "must-not-exist"), Dnsmasq: filepath.Join(dir, "must-not-run"),
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"serve", "--config", path}); err == nil || !strings.Contains(err.Error(), "host identity mismatch") {
		t.Fatalf("expected early host rejection, got %v", err)
	}
	if _, err := os.Stat(c.Output); !os.IsNotExist(err) {
		t.Fatalf("unsafe startup created output: %v", err)
	}
	if err := run([]string{"serve", "--config", path, "--marker", "/tmp/fake"}); err == nil {
		t.Fatal("ownership marker override accepted")
	}
}

func TestArgumentErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"render"}, {"serve", "--unknown"}} {
		if run(args) == nil {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
}
