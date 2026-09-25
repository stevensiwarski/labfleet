// Package bootstrap renders local NoCloud seed files for the LabFleet
// provisioner. It does not configure or contact a machine.
package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stevensiwarski/labfleet/provisioning/internal/pxe"
)

type Config struct {
	Hostname         string `json:"hostname"`
	ManagementMAC    string `json:"management_mac"`
	ProvisioningMAC  string `json:"provisioning_mac"`
	ProvisioningCIDR string `json:"provisioning_cidr"`
	Username         string `json:"username"`
	SSHPublicKeyFile string `json:"ssh_public_key_file"`
	Output           string `json:"output"`
	OwnershipTag     string `json:"ownership_tag"`
}

var hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var usernameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (c Config) Validate() error {
	if !hostnameRE.MatchString(c.Hostname) || !strings.HasPrefix(c.Hostname, "labfleet-") {
		return errors.New("hostname must be a labfleet-prefixed DNS label")
	}
	if !usernameRE.MatchString(c.Username) {
		return errors.New("invalid username")
	}
	if c.OwnershipTag != "labfleet" {
		return errors.New("ownership_tag must equal labfleet")
	}
	if !filepath.IsAbs(c.SSHPublicKeyFile) || !filepath.IsAbs(c.Output) || filepath.Clean(c.Output) == string(filepath.Separator) {
		return errors.New("SSH key and output paths must be safe absolute paths")
	}
	mac := func(s string) (net.HardwareAddr, error) {
		m, e := net.ParseMAC(s)
		if e == nil && (len(m) != 6 || m[0]&1 != 0 || m[0]&2 == 0) {
			e = errors.New("MAC must be local-unicast")
		}
		return m, e
	}
	a, e := mac(c.ManagementMAC)
	if e != nil {
		return fmt.Errorf("management_mac: %w", e)
	}
	b, e := mac(c.ProvisioningMAC)
	if e != nil {
		return fmt.Errorf("provisioning_mac: %w", e)
	}
	if bytes.Equal(a, b) {
		return errors.New("management and provisioning MACs must differ")
	}
	ip, network, e := net.ParseCIDR(c.ProvisioningCIDR)
	if e != nil || ip.To4() == nil || network == nil {
		return errors.New("provisioning_cidr must be IPv4 CIDR")
	}
	if !ip.Equal(ip.To4()) || !ip.IsGlobalUnicast() || ip.Equal(network.IP) {
		return errors.New("provisioning_cidr must contain a usable IPv4 host address")
	}
	bits, _ := network.Mask.Size()
	if bits < 1 || bits > 30 {
		return errors.New("provisioning_cidr must have a usable host range")
	}
	last := append(net.IP(nil), network.IP.To4()...)
	for i := range last {
		last[i] |= ^network.Mask[i]
	}
	if ip.Equal(last) {
		return errors.New("provisioning address cannot be broadcast")
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Config{}, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var c Config
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if e = d.Decode(&extra); e == nil {
		return c, errors.New("trailing JSON data")
	}
	if e != io.EOF {
		return c, errors.New("trailing or invalid JSON data")
	}
	return c, c.Validate()
}

// Render creates the three NoCloud seed files locally without contacting a machine.
func Render(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	key, err := pxe.ValidateSSHKey(c.SSHPublicKeyFile)
	if err != nil {
		return fmt.Errorf("SSH public key: %w", err)
	}
	parent := filepath.Dir(c.Output)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("output parent: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("output parent must be a real directory not writable by group or others (mode %s)", parentInfo.Mode())
	}
	if _, err := os.Lstat(c.Output); err == nil {
		return errors.New("output already exists; choose a fresh output path")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output path: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".seed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	q := func(s string) string { v, _ := json.Marshal(s); return string(v) }
	user := fmt.Sprintf(`#cloud-config
hostname: %s
manage_etc_hosts: true
disable_root: true
ssh_pwauth: false
groups:
  - labfleet-pxe
users:
  - default
  - name: %s
    lock_passwd: true
    groups: [sudo]
    shell: /bin/bash
    sudo: ["ALL=(ALL) NOPASSWD:ALL"]
    ssh_authorized_keys:
      - %s
package_update: true
package_upgrade: false
packages:
  - qemu-guest-agent
  - dnsmasq-base
  - ipxe
  - libarchive-tools
  - tcpdump
  - curl
  - ca-certificates
write_files:
  - path: /etc/labfleet/provisioning-owned
    defer: true
    owner: root:labfleet-pxe
    permissions: '0640'
    content: "labfleet\n"
  - path: /etc/sysctl.d/90-labfleet-provisioning.conf
    owner: root:root
    permissions: '0644'
    content: |
      net.ipv4.ip_forward = 0
      net.ipv6.conf.all.forwarding = 0
      net.ipv6.conf.default.forwarding = 0
runcmd:
  - [systemctl, enable, --now, qemu-guest-agent]
  - [groupadd, --system, --force, labfleet-pxe]
  - [useradd, --system, --gid, labfleet-pxe, --home-dir, /var/lib/labfleet/provisioning, --no-create-home, --shell, /usr/sbin/nologin, --, labfleet-pxe]
  - [install, -d, -o, root, -g, labfleet-pxe, -m, '0750', /etc/labfleet]
  - [install, -d, -o, labfleet-pxe, -g, labfleet-pxe, -m, '0750', /var/lib/labfleet/provisioning/runtime]
  - [install, -d, -o, root, -g, labfleet-pxe, -m, '0750', /var/lib/labfleet/provisioning/artifacts]
  - [sysctl, --load=/etc/sysctl.d/90-labfleet-provisioning.conf]
`, q(c.Hostname), q(c.Username), q(key))
	meta := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", q("labfleet-"+c.Hostname), q(c.Hostname))
	network := fmt.Sprintf("version: 2\nrenderer: networkd\nethernets:\n  mgmt0:\n    match:\n      macaddress: %s\n    set-name: mgmt0\n    dhcp4: true\n    dhcp6: false\n  prov0:\n    match:\n      macaddress: %s\n    set-name: prov0\n    dhcp4: false\n    dhcp6: false\n    addresses: [%s]\n", q(strings.ToLower(c.ManagementMAC)), q(strings.ToLower(c.ProvisioningMAC)), q(c.ProvisioningCIDR))
	for _, file := range []struct{ name, data string }{{"user-data", user}, {"meta-data", meta}, {"network-config", network}} {
		if err := atomicWrite(filepath.Join(tmp, file.name), []byte(file.data), 0644); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, c.Output); err != nil {
		return fmt.Errorf("publish seed output (choose a fresh output path): %w", err)
	}
	return nil
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(name, path)
}
