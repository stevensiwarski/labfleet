package pxe

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
)

// CheckHost validates the pre-provisioned service marker and active link without changing host state.
func CheckHost(c Config, hostname, marker string, interfaces []net.Interface, addrs map[string][]net.Addr, defaultIfaces map[string]bool) error {
	if hostname != c.ServiceHostname {
		return fmt.Errorf("host identity mismatch")
	}
	s, e := os.Lstat(marker)
	if e != nil {
		return fmt.Errorf("ownership marker unavailable: %w", e)
	}
	if !s.Mode().IsRegular() || s.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("ownership marker must be regular and not group/world writable")
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return fmt.Errorf("ownership marker must be root-owned")
	}
	b, e := os.ReadFile(marker)
	if e != nil || string(b) != "labfleet\n" {
		return fmt.Errorf("ownership marker content mismatch")
	}
	return CheckSnapshot(c, hostname, interfaces, addrs, defaultIfaces)
}

func CheckSnapshot(c Config, hostname string, interfaces []net.Interface, addrs map[string][]net.Addr, defaultIfaces map[string]bool) error {
	if hostname != c.ServiceHostname {
		return fmt.Errorf("host identity mismatch")
	}
	var found *net.Interface
	for i := range interfaces {
		if interfaces[i].Name == c.Interface {
			found = &interfaces[i]
			break
		}
	}
	if found == nil || found.Flags&net.FlagUp == 0 || found.Flags&net.FlagLoopback != 0 {
		return fmt.Errorf("provisioning interface unavailable or unsafe")
	}
	mac, e := net.ParseMAC(c.ExpectedMAC)
	if e != nil || found.HardwareAddr.String() != mac.String() {
		return fmt.Errorf("interface MAC mismatch")
	}
	if defaultIfaces[c.Interface] {
		return fmt.Errorf("provisioning interface carries a default route")
	}
	want, _, _ := net.ParseCIDR(c.Address)
	got := addrs[c.Interface]
	var foundIP *net.IPNet
	for _, addr := range got {
		n, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if n.IP.To4() != nil {
			if foundIP != nil {
				return fmt.Errorf("provisioning interface has extra IPv4 address")
			}
			foundIP = n
		} else if !n.IP.IsLinkLocalUnicast() {
			return fmt.Errorf("provisioning interface has unsafe IPv6 address")
		}
	}
	if foundIP == nil || !foundIP.IP.Equal(want) || foundIP.String() != c.Address {
		return fmt.Errorf("provisioning interface address mismatch")
	}
	return nil
}
func DefaultRoutes() (map[string]bool, error) {
	b, e := os.ReadFile("/proc/net/route")
	if e != nil {
		return nil, e
	}
	out := map[string]bool{}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 || len(strings.Fields(lines[0])) < 2 || strings.Fields(lines[0])[0] != "Iface" {
		return nil, fmt.Errorf("malformed IPv4 route table")
	}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 11 {
			return nil, fmt.Errorf("malformed IPv4 route entry")
		}
		if _, e := hex.DecodeString(f[1]); e != nil {
			return nil, fmt.Errorf("malformed IPv4 route destination")
		}
		if f[1] == "00000000" {
			out[f[0]] = true
		}
	}
	b, e = os.ReadFile("/proc/net/ipv6_route")
	if e != nil {
		return nil, e
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 10 {
			return nil, fmt.Errorf("malformed IPv6 route entry")
		}
		if _, e := hex.DecodeString(f[0]); e != nil || len(f[0]) != 32 {
			return nil, fmt.Errorf("malformed IPv6 route destination")
		}
		if f[0] == strings.Repeat("0", 32) && f[1] == "00" {
			out[f[9]] = true
		}
	}
	return out, nil
}
func LiveHostCheck(c Config, marker string) error {
	h, e := os.Hostname()
	if e != nil {
		return e
	}
	ifs, e := net.Interfaces()
	if e != nil {
		return e
	}
	a := map[string][]net.Addr{}
	for _, i := range ifs {
		a[i.Name], _ = i.Addrs()
	}
	routes, e := DefaultRoutes()
	if e != nil {
		return e
	}
	if e = CheckHost(c, h, marker, ifs, a, routes); e != nil {
		return e
	}
	for _, p := range []string{"/proc/sys/net/ipv4/ip_forward", "/proc/sys/net/ipv6/conf/all/forwarding"} {
		b, e := os.ReadFile(p)
		if e != nil || strings.TrimSpace(string(b)) != "0" {
			return fmt.Errorf("IP forwarding must be disabled (%s)", p)
		}
	}
	return nil
}
