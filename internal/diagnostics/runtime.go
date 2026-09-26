package diagnostics

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

var commandRunner runner.Runner = runner.Exec{}
var lookupIP = func(ctx context.Context, name string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, name)
}
var dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func command(ctx context.Context, name string, args ...string) (runner.Result, error) {
	return commandRunner.Run(ctx, runner.Command{Name: name, Args: args, Timeout: 3 * time.Second})
}
func serviceCheck(ctx context.Context, service string) Result {
	r, e := command(ctx, "systemctl", "is-active", service)
	if e == nil && strings.TrimSpace(string(r.Stdout)) == "active" {
		return Result{Name: service + " service", Status: Pass, Message: "service active"}
	}
	return failure(service+" service", "service is not active")
}
func timeSync(ctx context.Context) Result {
	r, e := command(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
	if e == nil && strings.TrimSpace(string(r.Stdout)) == "yes" {
		return Result{Name: "time synchronization", Status: Pass, Message: "system clock synchronized"}
	}
	r, e = command(ctx, "chronyc", "tracking")
	if e == nil {
		text := string(r.Stdout)
		if strings.Contains(text, "Leap status     : Normal") || strings.Contains(text, "Leap status: Normal") {
			return Result{Name: "time synchronization", Status: Pass, Message: "chrony reports normal synchronization"}
		}
	}
	return failure("time synchronization", "clock synchronization not confirmed")
}
func criCheck(ctx context.Context) Result {
	r, e := command(ctx, "ctr", "--address", "/run/containerd/containerd.sock", "plugins", "ls")
	if e != nil {
		return failure("containerd CRI", "CRI plugin health check failed")
	}
	if !parseCRIPlugins(string(r.Stdout)) {
		return failure("containerd CRI", "CRI plugin is not healthy")
	}
	return Result{Name: "containerd CRI", Status: Pass, Message: "CRI plugin healthy"}
}

func parseCRIPlugins(output string) bool {
	plugins := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		id, typ, status := f[0], f[1], f[len(f)-1]
		if status != "ok" {
			continue
		}
		if id == "io.containerd.cri.v1" && (typ == "runtime" || typ == "images") {
			plugins[typ] = true
		}
		if id == "io.containerd.grpc.v1.cri" {
			plugins["legacy"] = true
		}
	}
	return plugins["legacy"] || (plugins["runtime"] && plugins["images"])
}

func addNetworkChecks(add func(string, func(context.Context) Result), req ProbeRequest) {
	add("management interface", func(context.Context) Result {
		ips, e := interfaceIPv4("mgmt0")
		if e != nil || len(ips) == 0 {
			return failure("management interface", "mgmt0 has no active IPv4 address")
		}
		if req.ManagementIP != "" {
			for _, ip := range ips {
				if ip == req.ManagementIP {
					return Result{Name: "management interface", Status: Pass, Message: "management address matches inventory"}
				}
			}
			return failure("management interface", "management address does not match inventory")
		}
		return Result{Name: "management interface", Status: Pass, Message: "mgmt0 has active IPv4"}
	})
	add("default route", func(context.Context) Result {
		iface, gw, e := defaultRoute()
		if e != nil || iface != "mgmt0" {
			return failure("default route", "default route is not through mgmt0")
		}
		return Result{Name: "default route", Status: Pass, Message: "default route uses mgmt0", Details: map[string]any{"gateway": gw}}
	})
	add("gateway reachability", func(ctx context.Context) Result {
		_, gw, e := defaultRoute()
		if e != nil {
			return failure("gateway reachability", "default gateway unavailable")
		}
		ip := net.ParseIP(gw)
		if ip == nil {
			return failure("gateway reachability", "default gateway invalid")
		}
		r, e := command(ctx, "ping", "-n", "-c", "1", "-W", "2", ip.String())
		if e != nil {
			return failure("gateway reachability", "default gateway did not respond")
		}
		_ = r
		return Result{Name: "gateway reachability", Status: Pass, Message: "default gateway reachable"}
	})
	add("DNS", func(ctx context.Context) Result {
		start := time.Now()
		ips, e := lookupIP(ctx, req.DNSName)
		d := time.Since(start)
		if e != nil || len(ips) == 0 {
			return failure("DNS", "name resolution failed")
		}
		s, m := Pass, "name resolution healthy"
		if d > 2*time.Second {
			s, m = Fail, "DNS response too slow"
		} else if d > 500*time.Millisecond {
			s, m = Warn, "DNS response slow"
		}
		return Result{Name: "DNS", Status: s, Message: m, Details: map[string]any{"latency_ms": d.Milliseconds()}}
	})
	endpoints := append([]string(nil), req.TCPEndpoints...)
	if req.APIServer != "" {
		endpoints = append([]string{req.APIServer}, endpoints...)
	}
	for i, endpoint := range endpoints {
		ep := endpoint
		name := "TCP endpoint " + ep
		if i == 0 && req.APIServer != "" {
			name = "Kubernetes API"
		}
		add(name, func(ctx context.Context) Result {
			start := time.Now()
			c, e := dialContext(ctx, "tcp", ep)
			if e != nil {
				return failure(name, "TCP connection failed")
			}
			c.Close()
			return Result{Name: name, Status: Pass, Message: "TCP connection succeeded", Details: map[string]any{"latency_ms": time.Since(start).Milliseconds()}}
		})
	}
}

func interfaceIPv4(name string) ([]string, error) {
	iface, e := net.InterfaceByName(name)
	if e != nil {
		return nil, e
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, os.ErrInvalid
	}
	addrs, e := iface.Addrs()
	if e != nil {
		return nil, e
	}
	return usableIPv4Addresses(addrs), nil
}

func usableIPv4Addresses(addrs []net.Addr) []string {
	out := []string{}
	for _, a := range addrs {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() {
			out = append(out, ip.String())
		}
	}
	return out
}
