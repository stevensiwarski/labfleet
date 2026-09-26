package pxe

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type Config struct {
	ServiceHostname  string   `json:"service_hostname"`
	Interface        string   `json:"interface"`
	ExpectedMAC      string   `json:"expected_mac"`
	Address          string   `json:"address"`
	ServiceIP        string   `json:"service_ip"`
	TargetIP         string   `json:"target_ip"`
	Subnet           string   `json:"subnet"`
	TargetMAC        string   `json:"target_mac"`
	ManagementMAC    string   `json:"management_mac,omitempty"`
	PasswordlessSudo bool     `json:"passwordless_sudo"`
	TargetHostname   string   `json:"target_hostname"`
	Username         string   `json:"username"`
	DiskSerial       string   `json:"disk_serial"`
	SSHKeyPath       string   `json:"ssh_key_path"`
	Artifacts        string   `json:"artifacts"`
	Output           string   `json:"output"`
	HTTPPort         int      `json:"http_port"`
	Dnsmasq          string   `json:"dnsmasq"`
	Targets          []Target `json:"targets,omitempty"`
}

// Target identifies one isolated PXE client in a shared provisioning run.
type Target struct {
	TargetIP       string `json:"target_ip"`
	TargetMAC      string `json:"target_mac"`
	TargetHostname string `json:"target_hostname"`
	DiskSerial     string `json:"disk_serial"`
	ManagementMAC  string `json:"management_mac,omitempty"`
}

type Manifest struct {
	SHA256 map[string]string `json:"sha256"`
}

var artifactNames = []string{"ubuntu.iso", "vmlinuz", "initrd", "undionly.kpxe", "disk-select"}
var dnsName = regexp.MustCompile(`^labfleet-[a-z0-9](?:[a-z0-9-]{0,50}[a-z0-9])?$`)

const UbuntuISOURL = "https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso"
const UbuntuISOSHA256 = "97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8"

func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, errors.New("trailing or invalid JSON data")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if len(c.Targets) > 0 {
		if len(c.Targets) > 16 {
			return errors.New("at most 16 provisioning targets are supported")
		}
		if c.TargetIP != "" || c.TargetMAC != "" || c.TargetHostname != "" || c.DiskSerial != "" || c.ManagementMAC != "" {
			return errors.New("fleet targets cannot be combined with singular target fields")
		}
		ips, macs, hosts, disks := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
		for i, target := range c.Targets {
			one := c
			one.Targets = nil
			one.TargetIP, one.TargetMAC, one.TargetHostname, one.DiskSerial, one.ManagementMAC = target.TargetIP, target.TargetMAC, target.TargetHostname, target.DiskSerial, target.ManagementMAC
			if err := one.validateSingle(); err != nil {
				return fmt.Errorf("target %d: %w", i, err)
			}
			for _, value := range []string{target.TargetMAC, target.ManagementMAC} {
				if value == "" {
					continue
				}
				parsed, _ := net.ParseMAC(value) // validateSingle already validated each MAC.
				canonical := parsed.String()
				if macs[canonical] {
					return fmt.Errorf("duplicate fleet NIC MAC %q", canonical)
				}
				macs[canonical] = true
			}
			for _, entry := range []struct {
				name, value string
				seen        map[string]bool
			}{{"IP", target.TargetIP, ips}, {"hostname", target.TargetHostname, hosts}, {"disk serial", target.DiskSerial, disks}} {
				if entry.seen[entry.value] {
					return fmt.Errorf("duplicate target %s %q", entry.name, entry.value)
				}
				entry.seen[entry.value] = true
			}
		}
		return nil
	}
	return c.validateSingle()
}

func (c Config) validateSingle() error {
	if !dnsName.MatchString(c.ServiceHostname) || !dnsName.MatchString(c.TargetHostname) {
		return errors.New("service and target hostnames must be labfleet-* DNS labels")
	}
	if c.ServiceHostname == c.TargetHostname {
		return errors.New("service and target hostnames must be distinct")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`).MatchString(c.Interface) || strings.Contains(c.Interface, "..") || c.Interface == "lo" || strings.Contains(c.Interface, ":") {
		return errors.New("invalid interface name")
	}
	mac := func(s string) (net.HardwareAddr, error) {
		m, e := net.ParseMAC(s)
		if e == nil && (len(m) != 6 || m[0]&1 != 0 || m[0]&2 == 0) {
			e = errors.New("MAC must be explicit local-unicast")
		}
		return m, e
	}
	serverMAC, e := mac(c.ExpectedMAC)
	if e != nil {
		return fmt.Errorf("expected_mac: %w", e)
	}
	targetMAC, e := mac(c.TargetMAC)
	if e != nil {
		return fmt.Errorf("target_mac: %w", e)
	}
	if serverMAC.String() == targetMAC.String() {
		return errors.New("target MAC equals service MAC")
	}
	if c.ManagementMAC != "" {
		managementMAC, err := mac(c.ManagementMAC)
		if err != nil {
			return fmt.Errorf("management_mac: %w", err)
		}
		if managementMAC.String() == targetMAC.String() {
			return errors.New("management MAC equals target MAC")
		}
	}
	_, network, e := net.ParseCIDR(c.Subnet)
	if e != nil || network == nil || network.IP.To4() == nil {
		return errors.New("subnet must be IPv4 CIDR")
	}
	if network.IP.To4().String() != strings.Split(c.Subnet, "/")[0] {
		return errors.New("subnet must be canonical")
	}
	server := net.ParseIP(c.ServiceIP).To4()
	target := net.ParseIP(c.TargetIP).To4()
	ifaceIP, _, e := net.ParseCIDR(c.Address)
	if server == nil || target == nil || e != nil || ifaceIP.To4() == nil || !network.Contains(server) || !network.Contains(target) || !network.Contains(ifaceIP) {
		return errors.New("service, target, and interface address must be IPv4 addresses in subnet")
	}
	if c.ServiceIP != server.String() || c.TargetIP != target.String() {
		return errors.New("service and target IPs must use canonical IPv4 notation")
	}
	if server.Equal(target) || server.Equal(network.IP) || target.Equal(network.IP) || !server.IsGlobalUnicast() || !target.IsGlobalUnicast() || !ifaceIP.To4().IsGlobalUnicast() {
		return errors.New("service/target must be distinct usable addresses")
	}
	ones := 0
	for _, b := range network.Mask {
		for i := 0; i < 8; i++ {
			if b&(1<<uint(i)) != 0 {
				ones++
			}
		}
	}
	if ones > 30 {
		return errors.New("subnet must have at least two host addresses")
	}
	last := append(net.IP(nil), network.IP.To4()...)
	for i := range last {
		last[i] |= ^network.Mask[i]
	}
	if server.Equal(last) || target.Equal(last) {
		return errors.New("service/target cannot be broadcast")
	}
	if !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(c.Username) || len(c.DiskSerial) == 0 || len(c.DiskSerial) > 128 || !strings.Contains(c.DiskSerial, "labfleet-") || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(c.DiskSerial) {
		return errors.New("invalid UNIX username or disk serial (up to 128 ASCII bytes, must contain labfleet-)")
	}
	if !filepath.IsAbs(c.Artifacts) || !filepath.IsAbs(c.Output) || !filepath.IsAbs(c.SSHKeyPath) {
		return errors.New("artifact, output, and SSH key paths must be absolute")
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		return errors.New("http_port must be 1..65535")
	}
	if !filepath.IsAbs(c.Dnsmasq) {
		return errors.New("dnsmasq executable must be an absolute path")
	}
	for _, p := range []string{c.Artifacts, c.Output, c.SSHKeyPath, c.Interface, c.ServiceHostname, c.TargetHostname, c.TargetMAC, c.DiskSerial} {
		if p == "" || strings.ContainsAny(p, "\r\n, #\t") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
			return errors.New("unsafe configuration value")
		}
	}
	if !server.Equal(ifaceIP.To4()) {
		return errors.New("service_ip must equal interface address")
	}
	_, addressNet, _ := net.ParseCIDR(c.Address)
	if addressNet == nil || !bytes.Equal(addressNet.Mask, network.Mask) {
		return errors.New("interface and service subnet prefixes must match")
	}
	if filepath.Clean(c.Output) == filepath.Clean(c.Artifacts) || strings.HasPrefix(filepath.Clean(c.Output)+string(os.PathSeparator), filepath.Clean(c.Artifacts)+string(os.PathSeparator)) || strings.HasPrefix(filepath.Clean(c.Artifacts)+string(os.PathSeparator), filepath.Clean(c.Output)+string(os.PathSeparator)) {
		return errors.New("output and artifact directories must not overlap")
	}
	return nil
}
func ReadAndVerifyManifest(dir string) error {
	return VerifyManifest(dir, UbuntuISOSHA256)
}
func regularNoSymlink(path string) error {
	s, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !s.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular non-symlink file", path)
	}
	return nil
}
func VerifyManifest(dir, pinnedISO string) error {
	ds, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	if !ds.IsDir() || ds.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact path must be a real directory")
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if e := regularNoSymlink(manifestPath); e != nil {
		return e
	}
	b, e := os.ReadFile(manifestPath)
	if e != nil {
		return e
	}
	var m Manifest
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing manifest data")
	}
	for _, name := range artifactNames {
		want, ok := m.SHA256[name]
		if !ok || len(want) != 64 {
			return fmt.Errorf("manifest missing SHA256 for %s", name)
		}
		expected, e := hex.DecodeString(want)
		if e != nil || len(expected) != sha256.Size {
			return fmt.Errorf("invalid SHA256 for %s", name)
		}
		if name == "ubuntu.iso" && !strings.EqualFold(want, pinnedISO) {
			return errors.New("Ubuntu ISO manifest digest does not match pinned release")
		}
		artifactPath := filepath.Join(dir, name)
		if e = regularNoSymlink(artifactPath); e != nil {
			return e
		}
		f, e := os.Open(artifactPath)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return e
		}
		if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
			return fmt.Errorf("SHA256 mismatch for %s", name)
		}
	}
	return nil
}

func ValidateSSHKey(path string) (string, error) {
	if e := regularNoSymlink(path); e != nil {
		return "", e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	line := strings.TrimSuffix(string(b), "\n")
	if strings.ContainsAny(strings.TrimSuffix(line, "\r"), "\r\n") {
		return "", errors.New("exactly one SSH key line required")
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return "", errors.New("invalid SSH public key")
	}
	raw, e := base64.StdEncoding.DecodeString(fields[1])
	if e != nil || len(raw) < 4 {
		return "", errors.New("invalid SSH public key encoding")
	}
	n := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
	if n <= 0 || 4+n+4+32 != len(raw) || string(raw[4:4+n]) != fields[0] || int(raw[4+n])<<24|int(raw[5+n])<<16|int(raw[6+n])<<8|int(raw[7+n]) != 32 {
		return "", errors.New("SSH key algorithm does not match wire encoding")
	}
	return fields[0] + " " + fields[1], nil
}
