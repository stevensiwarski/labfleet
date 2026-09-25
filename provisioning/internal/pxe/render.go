package pxe

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

func networkConfig(c Config) string {
	if c.ManagementMAC == "" {
		return ""
	}
	return fmt.Sprintf(`  network:
    version: 2
    ethernets:
      mgmt0:
        match:
          macaddress: %s
        set-name: mgmt0
        dhcp4: true
        dhcp6: false
        accept-ra: false
        dhcp4-overrides:
          route-metric: 100
      prov0:
        match:
          macaddress: %s
        set-name: prov0
        dhcp4: true
        dhcp6: false
        accept-ra: false
        dhcp4-overrides:
          use-routes: false
          use-dns: false
          route-metric: 1000
        optional: true`, q(c.ManagementMAC), q(c.TargetMAC))
}

func packageConfig(c Config) string {
	if c.ManagementMAC == "" {
		return ""
	}
	return "  packages:\n    - qemu-guest-agent"
}

func sudoLateCommand(c Config) string {
	commands := []string{}
	if c.ManagementMAC != "" {
		commands = append(commands, "[curtin, in-target, --target=/target, --, systemctl, enable, qemu-guest-agent]")
	}
	if c.PasswordlessSudo {
		command := fmt.Sprintf("install -o root -g root -m 0440 /dev/null /etc/sudoers.d/90-labfleet-fleet && printf '%%s\\n' '%s ALL=(ALL:ALL) NOPASSWD: ALL' > /etc/sudoers.d/90-labfleet-fleet && visudo -cf /etc/sudoers.d/90-labfleet-fleet && visudo -c", c.Username)
		commands = append(commands, "[curtin, in-target, --target=/target, --, sh, -c, "+q(command)+"]")
	}
	if len(commands) == 0 {
		return ""
	}
	result := "  late-commands:\n"
	for _, command := range commands {
		result += "    - " + command + "\n"
	}
	return strings.TrimSuffix(result, "\n")
}

func Render(c Config) (map[string][]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	key, e := ValidateSSHKey(c.SSHKeyPath)
	if e != nil {
		return nil, e
	}
	base := fmt.Sprintf("http://%s:%d", c.ServiceIP, c.HTTPPort)
	bootif := ""
	if c.ManagementMAC != "" {
		mac, _ := net.ParseMAC(c.TargetMAC)
		bootif = "BOOTIF=01-" + strings.ReplaceAll(strings.ToLower(mac.String()), ":", "-") + " "
	}
	boot := fmt.Sprintf(`#!ipxe
sanboot --no-describe --drive 0x80 || goto install
:install
kernel %s/vmlinuz initrd=initrd autoinstall %sip=dhcp url=%s/ubuntu.iso ds=nocloud-net;s=%s/seed/ cloud-config-url=/dev/null
initrd %s/initrd
boot
`, base, bootif, base, base, base)
	user := fmt.Sprintf(`#cloud-config
autoinstall:
  version: 1
%s
%s
  interactive-sections: []
  early-commands:
    - [sh, -c, %s]
%s
  refresh-installer:
    update: false
  apt:
    geoip: false
    fallback: offline-install
  locale: %s
  keyboard:
    layout: us
  identity:
    hostname: %s
    username: %s
    password: "!"
  ssh:
    install-server: true
    allow-pw: false
    authorized-keys:
      - %s
  user-data:
    disable_root: true
    ssh_pwauth: false
  storage:
    layout:
      name: direct
      match:
        serial: %s
  shutdown: reboot
`, networkConfig(c), packageConfig(c), q(fmt.Sprintf("wget -q -O /usr/local/sbin/labfleet-disk-select %s/disk-select && chmod 0700 /usr/local/sbin/labfleet-disk-select && /usr/local/sbin/labfleet-disk-select --expected-id %s --autoinstall /autoinstall.yaml", base, c.DiskSerial)), sudoLateCommand(c), q("en_US.UTF-8"), q(c.TargetHostname), q(c.Username), q(key), q(c.DiskSerial))
	meta := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", q(c.TargetHostname), q(c.TargetHostname))
	_, network, _ := net.ParseCIDR(c.Subnet)
	mask := net.IP(network.Mask).String()
	targetMAC, _ := net.ParseMAC(c.TargetMAC)
	dns := fmt.Sprintf(`no-hosts
no-resolv
no-poll
interface=%s
except-interface=lo
bind-interfaces
listen-address=%s
port=0
enable-tftp=%s
tftp-root=%s
dhcp-authoritative
dhcp-leasefile=%s
pid-file=%s
log-dhcp
log-facility=-
dhcp-host=%s,%s,%s,infinite
dhcp-ignore=tag:!known
dhcp-range=set:labfleet,%s,static,%s
dhcp-option=option:router
dhcp-option=option:dns-server
dhcp-userclass=set:ipxe,iPXE
dhcp-boot=tag:!ipxe,undionly.kpxe
dhcp-boot=tag:ipxe,http://%s:%d/boot.ipxe
`, c.Interface, c.ServiceIP, c.Interface, c.Artifacts, filepath.Join(c.Output, "dnsmasq.leases"), filepath.Join(c.Output, "dnsmasq.pid"), targetMAC, c.TargetIP, c.TargetHostname, network.IP, mask, c.ServiceIP, c.HTTPPort)
	return map[string][]byte{"boot.ipxe": []byte(boot), "user-data": []byte(user), "meta-data": []byte(meta), "dnsmasq.conf": []byte(dns)}, nil
}
func WriteRendered(dir string, files map[string][]byte) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("output directory must be absolute")
	}
	if e := os.MkdirAll(dir, 0750); e != nil {
		return e
	}
	ds, e := os.Lstat(dir)
	if e != nil || !ds.IsDir() || ds.Mode()&os.ModeSymlink != 0 || ds.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("output directory must be a non-writable-by-others real directory")
	}
	for n, b := range files {
		if strings.Contains(n, "/") {
			return fmt.Errorf("invalid output name")
		}
		f, e := os.OpenFile(filepath.Join(dir, n), os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return e
		}
		if e = f.Chmod(0600); e == nil {
			_, e = f.Write(b)
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return e
		}
	}
	return nil
}
