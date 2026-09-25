package pxe

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigRejectsInjectionAndInvalidTarget(t *testing.T) {
	c := testConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Interface = "eth0\ninterface=any"
	if c.Validate() == nil {
		t.Fatal("accepted config injection")
	}
	c.Interface = "enp2s0"
	c.TargetIP = "192.0.2.255"
	if c.Validate() == nil {
		t.Fatal("accepted broadcast target")
	}
}

func TestOptionalManagementNICAndPasswordlessSudoRendering(t *testing.T) {
	c := testConfig()
	c.SSHKeyPath = writeKey(t)
	files, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	base := string(files["user-data"])
	if strings.Contains(base, "network:\n") || strings.Contains(base, "NOPASSWD") || strings.Contains(base, "qemu-guest-agent") {
		t.Fatal("default config must retain single-NIC and no-sudo behavior without guest agent")
	}
	c.ManagementMAC = "02:00:00:00:00:03"
	c.PasswordlessSudo = true
	files, err = Render(c)
	if err != nil {
		t.Fatal(err)
	}
	u := string(files["user-data"])
	for _, want := range []string{"version: 2", `macaddress: "02:00:00:00:00:03"`, `macaddress: "02:00:00:00:00:02"`, "route-metric: 100", "route-metric: 1000", "use-routes: false", "use-dns: false", "optional: true", "qemu-guest-agent", "systemctl, enable, qemu-guest-agent", "90-labfleet-fleet", "fleet ALL=(ALL:ALL) NOPASSWD: ALL", "disable_root: true", "allow-pw: false", "ssh_pwauth: false", "visudo -c"} {
		if !strings.Contains(u, want) {
			t.Errorf("opt-in render missing %q", want)
		}
	}
	if !strings.Contains(string(files["boot.ipxe"]), "BOOTIF=01-02-00-00-00-00-02 ip=dhcp") {
		t.Fatalf("dual-NIC initramfs must select the provisioning NIC by its MAC: %s", files["boot.ipxe"])
	}
	for _, invalid := range []string{"02:00:00:00:00:02", "01:00:00:00:00:03", "00:00:00:00:00:03", "not-a-mac"} {
		bad := c
		bad.ManagementMAC = invalid
		if bad.Validate() == nil {
			t.Errorf("accepted invalid management MAC %q", invalid)
		}
	}
}

func testConfig() Config {
	return Config{ServiceHostname: "labfleet-svc", Interface: "enp2s0", ExpectedMAC: "02:00:00:00:00:01", Address: "192.0.2.1/24", ServiceIP: "192.0.2.1", TargetIP: "192.0.2.2", Subnet: "192.0.2.0/24", TargetMAC: "02:00:00:00:00:02", TargetHostname: "labfleet-node", Username: "fleet", DiskSerial: "0QEMU_QEMU_HARDDISK_labfleet-pxe-930004", SSHKeyPath: "/tmp/key", Artifacts: "/tmp/artifacts", Output: "/tmp/out", HTTPPort: 8080, Dnsmasq: "/usr/sbin/dnsmasq"}
}

func writeKey(t *testing.T) string {
	t.Helper()
	pub, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	raw := make([]byte, 4+len("ssh-ed25519")+4+len(pub))
	binary.BigEndian.PutUint32(raw, uint32(len("ssh-ed25519")))
	copy(raw[4:], "ssh-ed25519")
	off := 4 + len("ssh-ed25519")
	binary.BigEndian.PutUint32(raw[off:], uint32(len(pub)))
	copy(raw[off+4:], pub)
	p := filepath.Join(t.TempDir(), "key.pub")
	if e = os.WriteFile(p, []byte("ssh-ed25519 "+base64.StdEncoding.EncodeToString(raw)+" test-key\n"), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestRenderIsOfflineAndPinnedTargetsAreExact(t *testing.T) {
	c := testConfig()
	c.SSHKeyPath = writeKey(t)
	files, e := Render(c)
	if e != nil {
		t.Fatal(e)
	}
	again, e := Render(c)
	if e != nil || !reflect.DeepEqual(files, again) {
		t.Fatal("rendering with the same configuration must be deterministic")
	}
	boot := string(files["boot.ipxe"])
	if !strings.Contains(boot, "sanboot --no-describe --drive 0x80 || goto install") || !strings.Contains(boot, "ds=nocloud-net;s=") {
		t.Fatalf("boot fallback/seed missing: %s", boot)
	}
	if strings.Contains(boot, "callback") {
		t.Fatal("unexpected callback")
	}
	if strings.Contains(boot, "BOOTIF=") {
		t.Fatal("single-NIC boot must retain the existing DHCP selection")
	}
	u := string(files["user-data"])
	if !strings.Contains(u, "early-commands:") || !strings.Contains(u, "/disk-select") || !strings.Contains(u, "chmod 0700") || !strings.Contains(u, "--expected-id "+c.DiskSerial+" --autoinstall /autoinstall.yaml") {
		t.Fatal("missing fail-closed exact disk identity resolution")
	}
	if strings.Contains(u, "/run/labfleet-disk-select") || !strings.Contains(u, "/usr/local/sbin/labfleet-disk-select") {
		t.Fatal("installer /run is noexec; helper must use executable live-root path")
	}
	if !strings.Contains(u, "name: direct") || !strings.Contains(u, "serial: \"0QEMU_QEMU_HARDDISK_labfleet-pxe-930004\"") || strings.Contains(u, "type: disk") {
		t.Fatalf("not exact direct layout: %s", u)
	}
	dns := string(files["dnsmasq.conf"])
	for _, s := range []string{"interface=enp2s0", "dhcp-range=set:labfleet,192.0.2.0,static,255.255.255.0", "dhcp-option=option:router\n", "dhcp-option=option:dns-server\n", "no-resolv", "no-hosts"} {
		if !strings.Contains(dns, s) {
			t.Errorf("missing dnsmasq directive %q", s)
		}
	}
}

func TestSSHKeyRejectsInvalidWireData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(p, []byte("ssh-ed25519 YWJj\n"), 0600)
	if _, e := ValidateSSHKey(p); e == nil {
		t.Fatal("accepted malformed key")
	}
	p2 := filepath.Join(t.TempDir(), "private")
	_ = os.WriteFile(p2, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"), 0600)
	if _, e := ValidateSSHKey(p2); e == nil {
		t.Fatal("accepted private key")
	}
}

func TestPinnedISOHashMustMatch(t *testing.T) {
	d := t.TempDir()
	for _, n := range artifactNames {
		_ = os.WriteFile(filepath.Join(d, n), []byte("data"), 0600)
	}
	sha := "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7"
	manifest := `{"sha256":{"ubuntu.iso":"` + sha + `","vmlinuz":"` + sha + `","initrd":"` + sha + `","undionly.kpxe":"` + sha + `","disk-select":"` + sha + `"}}`
	_ = os.WriteFile(filepath.Join(d, "manifest.json"), []byte(manifest), 0600)
	if VerifyManifest(d, UbuntuISOSHA256) == nil {
		t.Fatal("accepted non-release ISO hash")
	}
	if VerifyManifest(d, sha) != nil {
		t.Fatal("fixture digest should verify against explicit test pin")
	}
	if e := os.WriteFile(filepath.Join(d, "vmlinuz"), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if VerifyManifest(d, sha) == nil {
		t.Fatal("accepted modified kernel bytes")
	}
	if e := os.WriteFile(filepath.Join(d, "vmlinuz"), []byte("data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d, "disk-select"), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if VerifyManifest(d, sha) == nil {
		t.Fatal("accepted modified disk identity helper")
	}
}

func TestGeneratedDnsmasqConfigParses(t *testing.T) {
	bin, e := exec.LookPath("/usr/sbin/dnsmasq")
	if e != nil {
		t.Skip("dnsmasq not installed")
	}
	c := testConfig()
	c.SSHKeyPath = writeKey(t)
	c.Output = t.TempDir()
	if e = os.Chmod(c.Output, 0700); e != nil {
		t.Fatal(e)
	}
	files, e := Render(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = WriteRendered(c.Output, files); e != nil {
		t.Fatal(e)
	}
	out, e := exec.Command(bin, "--test", "--conf-file="+filepath.Join(c.Output, "dnsmasq.conf")).CombinedOutput()
	if e != nil {
		t.Fatalf("dnsmasq rejected rendered config: %v: %s", e, out)
	}
}
func TestManifestMustMatchAllArtifacts(t *testing.T) {
	d := t.TempDir()
	for _, n := range artifactNames {
		if e := os.WriteFile(filepath.Join(d, n), []byte("data"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if os.WriteFile(filepath.Join(d, "manifest.json"), []byte(`{"sha256":{}}`), 0600) != nil {
		t.Fatal("write")
	}
	if ReadAndVerifyManifest(d) == nil {
		t.Fatal("accepted incomplete manifest")
	}
}
func TestHTTPAllowlistAndTargetSource(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"boot.ipxe", "user-data", "meta-data"} {
		_ = os.WriteFile(filepath.Join(d, n), []byte("content"), 0600)
	}
	art := t.TempDir()
	_ = os.WriteFile(filepath.Join(art, "ubuntu.iso"), []byte("iso"), 0600)
	c := Config{TargetIP: "192.0.2.2", Output: d, Artifacts: art}
	h := Handler(c)
	for _, tc := range []struct {
		method, path, remote string
		status               int
	}{{"GET", "/seed/user-data", "192.0.2.2:1", 200}, {"HEAD", "/seed/user-data", "192.0.2.2:1", 200}, {"GET", "/seed/user-data", "192.0.2.3:1", 403}, {"GET", "/secret", "192.0.2.2:1", 404}, {"GET", "/../secret", "192.0.2.2:1", 404}, {"GET", "/ubuntu.iso", "192.0.2.3:1", 403}} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.RemoteAddr = tc.remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s: got %d", tc.path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/seed/user-data", nil)
	r.RemoteAddr = "192.0.2.2:1"
	r.Header.Set("Range", "bytes=0-2")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 {
		t.Errorf("range status %d", w.Code)
	}
	symlink := filepath.Join(d, "boot.ipxe-link")
	if e := os.Symlink(filepath.Join(d, "boot.ipxe"), symlink); e != nil {
		t.Fatal(e)
	}
	_ = symlink // routes are fixed allowlist; a symlinked allowlisted path is rejected below.
	_ = os.Remove(filepath.Join(d, "user-data"))
	if e := os.Symlink(filepath.Join(d, "boot.ipxe"), filepath.Join(d, "user-data")); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/seed/user-data", nil)
	r.RemoteAddr = "192.0.2.2:1"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("followed symlink: %d", w.Code)
	}
}
func TestHostSnapshotFailsClosed(t *testing.T) {
	m := filepath.Join(t.TempDir(), "marker")
	if e := os.WriteFile(m, []byte("labfleet\n"), 0600); e != nil {
		t.Fatal(e)
	}
	st, e := net.InterfaceByName("lo")
	if e != nil {
		t.Fatal(e)
	}
	c := Config{ServiceHostname: "labfleet-svc", Interface: st.Name}
	if CheckHost(c, "labfleet-svc", m, []net.Interface{*st}, nil, map[string]bool{}) == nil {
		t.Fatal("accepted loopback")
	}
	if strings.Contains(m, "\n") {
		t.Fatal("unexpected")
	}
}

func TestHostSnapshotAcceptsExactIsolatedLinkAndRejectsDefaultRoute(t *testing.T) {
	c := testConfig()
	itf := net.Interface{Name: c.Interface, HardwareAddr: mustMAC(t, c.ExpectedMAC), Flags: net.FlagUp | net.FlagBroadcast}
	ip, subnet, e := net.ParseCIDR(c.Address)
	if e != nil {
		t.Fatal(e)
	}
	addrs := map[string][]net.Addr{c.Interface: {&net.IPNet{IP: ip, Mask: subnet.Mask}}}
	if e = CheckSnapshot(c, c.ServiceHostname, []net.Interface{itf}, addrs, map[string]bool{}); e != nil {
		t.Fatalf("valid snapshot rejected: %v", e)
	}
	if e = CheckSnapshot(c, c.ServiceHostname, []net.Interface{itf}, addrs, map[string]bool{c.Interface: true}); e == nil {
		t.Fatal("accepted default route on provisioning link")
	}
	wrong := itf
	wrong.HardwareAddr = net.HardwareAddr{2, 0, 0, 0, 0, 9}
	if e = CheckSnapshot(c, c.ServiceHostname, []net.Interface{wrong}, addrs, map[string]bool{}); e == nil {
		t.Fatal("accepted unexpected interface MAC")
	}
}

func mustMAC(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	m, e := net.ParseMAC(s)
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func TestSystemRoutesParseWithoutMutation(t *testing.T) {
	// Linux emits a tab-separated header; accepting only "Iface " regresses
	// every real host even if string-based route fixtures appear valid.
	if _, err := DefaultRoutes(); err != nil {
		t.Fatalf("read-only route-table parse failed: %v", err)
	}
}

func TestHostSnapshotRejectsIndividualUnsafeStates(t *testing.T) {
	c := testConfig()
	mac := mustMAC(t, c.ExpectedMAC)
	ip, subnet, _ := net.ParseCIDR(c.Address)
	good := net.Interface{Name: c.Interface, HardwareAddr: mac, Flags: net.FlagUp | net.FlagBroadcast}
	address := &net.IPNet{IP: ip, Mask: subnet.Mask}
	for _, tc := range []struct {
		name      string
		host      string
		iface     net.Interface
		addresses []net.Addr
	}{
		{"wrong host", "bootstrap", good, []net.Addr{address}},
		{"down link", c.ServiceHostname, net.Interface{Name: c.Interface, HardwareAddr: mac}, []net.Addr{address}},
		{"loopback", c.ServiceHostname, net.Interface{Name: c.Interface, HardwareAddr: mac, Flags: net.FlagUp | net.FlagLoopback}, []net.Addr{address}},
		{"wrong link", c.ServiceHostname, net.Interface{Name: "other", HardwareAddr: mac, Flags: net.FlagUp}, []net.Addr{address}},
		{"no address", c.ServiceHostname, good, nil},
		{"extra address", c.ServiceHostname, good, []net.Addr{address, address}},
		{"wrong prefix", c.ServiceHostname, good, []net.Addr{&net.IPNet{IP: ip, Mask: net.CIDRMask(16, 32)}}},
		{"global IPv6", c.ServiceHostname, good, []net.Addr{address, &net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if CheckSnapshot(c, tc.host, []net.Interface{tc.iface}, map[string][]net.Addr{c.Interface: tc.addresses}, nil) == nil {
				t.Fatal("unsafe host accepted")
			}
		})
	}
}
