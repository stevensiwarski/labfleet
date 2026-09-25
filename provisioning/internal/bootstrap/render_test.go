package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA test\n"

func fixture(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "id.pub")
	if err := os.WriteFile(key, []byte(validKey), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Hostname: "labfleet-provisioner", ManagementMAC: "02:00:00:00:00:01", ProvisioningMAC: "02:00:00:00:00:02", ProvisioningCIDR: "192.0.2.10/24", Username: "fleet", SSHPublicKeyFile: key, Output: filepath.Join(dir, "seed"), OwnershipTag: "labfleet"}
}

func TestRenderSeedContents(t *testing.T) {
	c := fixture(t)
	if err := Render(c); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, name := range []string{"user-data", "meta-data", "network-config"} {
		b, e := os.ReadFile(filepath.Join(c.Output, name))
		if e != nil {
			t.Fatal(e)
		}
		files[name] = string(b)
	}
	for _, want := range []string{"#cloud-config", "hostname: \"labfleet-provisioner\"", "disable_root: true", "ssh_pwauth: false", "ssh_authorized_keys:", "package_upgrade: false", "dnsmasq-base", "labfleet-pxe", "/etc/labfleet/provisioning-owned", "net.ipv4.ip_forward = 0", "net.ipv6.conf.default.forwarding = 0"} {
		if !strings.Contains(files["user-data"], want) {
			t.Errorf("user-data missing %q", want)
		}
	}
	net := files["network-config"]
	for _, want := range []string{"mgmt0:", "dhcp4: true", "prov0:", "192.0.2.10/24", "dhcp4: false"} {
		if !strings.Contains(net, want) {
			t.Errorf("network-config missing %q", want)
		}
	}
	mgmt := net[strings.Index(net, "mgmt0:"):strings.Index(net, "prov0:")]
	if !strings.Contains(mgmt, "dhcp4: true") {
		t.Fatal("management NIC must use DHCP")
	}
	prov := net[strings.Index(net, "prov0:"):]
	if strings.Contains(prov, "dhcp4: true") || strings.Contains(prov, "gateway") || strings.Contains(prov, "nameservers") {
		t.Fatal("provisioning NIC has dynamic configuration")
	}
	if !strings.Contains(files["meta-data"], "instance-id:") {
		t.Fatal("missing instance-id")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"ownership", func(c *Config) { c.OwnershipTag = "other" }},
		{"same MAC", func(c *Config) { c.ProvisioningMAC = c.ManagementMAC }},
		{"multicast MAC", func(c *Config) { c.ManagementMAC = "03:00:00:00:00:01" }},
		{"network address", func(c *Config) { c.ProvisioningCIDR = "192.0.2.0/24" }},
		{"broadcast", func(c *Config) { c.ProvisioningCIDR = "192.0.2.255/24" }},
		{"default route", func(c *Config) { c.ProvisioningCIDR = "0.0.0.0/0" }},
		{"loopback", func(c *Config) { c.ProvisioningCIDR = "127.0.0.1/24" }},
		{"multicast", func(c *Config) { c.ProvisioningCIDR = "224.0.0.1/24" }},
		{"relative output", func(c *Config) { c.Output = "relative" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fixture(t)
			tt.edit(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected invalid config")
			}
		})
	}
}

func TestRenderRejectsMalformedPublicKey(t *testing.T) {
	c := fixture(t)
	if err := os.WriteFile(c.SSHPublicKeyFile, []byte("ssh-rsa invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Render(c); err == nil {
		t.Fatal("expected malformed key rejection")
	}
}

func TestRenderRejectsExistingOutputWithoutChangingIt(t *testing.T) {
	c := fixture(t)
	if err := os.Mkdir(c.Output, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(c.Output, "keep")
	if err := os.WriteFile(marker, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Render(c); err == nil {
		t.Fatal("expected existing output rejection")
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "existing" {
		t.Fatalf("existing output changed: %q, %v", b, err)
	}
}

func TestRenderRejectsSymlinkOutput(t *testing.T) {
	c := fixture(t)
	target := filepath.Join(filepath.Dir(c.Output), "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, c.Output); err != nil {
		t.Fatal(err)
	}
	if err := Render(c); err == nil {
		t.Fatal("expected symlink output rejection")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target modified: %v, %v", entries, err)
	}
}
