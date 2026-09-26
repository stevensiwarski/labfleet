package diagnostics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func addHostChecks(add func(string, func(context.Context) Result), req ProbeRequest) {
	add("cpu", func(context.Context) Result {
		b, e := os.ReadFile("/proc/loadavg")
		if e != nil {
			return failure("cpu", "load data unavailable")
		}
		f := strings.Fields(string(b))
		if len(f) < 1 {
			return failure("cpu", "load data unavailable")
		}
		v, e := strconv.ParseFloat(f[0], 64)
		if e != nil {
			return failure("cpu", "load data invalid")
		}
		cpus := runtime.NumCPU()
		normalized := v / float64(cpus)
		s, m := threshold(normalized, 1, 2)
		return Result{Name: "cpu", Status: s, Message: m, Details: map[string]any{"load_1m": v, "normalized_load_1m": normalized, "cpu_count": cpus}}
	})
	add("CPU pressure", func(context.Context) Result {
		return pressureCheck("/proc/pressure/cpu", "some", "CPU pressure", 20, 50)
	})
	add("memory pressure", func(context.Context) Result {
		return pressureCheck("/proc/pressure/memory", "full", "memory pressure", 5, 20)
	})
	add("memory", func(context.Context) Result {
		f, e := os.Open("/proc/meminfo")
		if e != nil {
			return failure("memory", "memory data unavailable")
		}
		vals, e := parseMeminfo(f)
		f.Close()
		if e != nil || vals["MemTotal"] == 0 {
			return failure("memory", "memory data unavailable")
		}
		pct := 100 * float64(vals["MemAvailable"]) / float64(vals["MemTotal"])
		s, m := Pass, "available memory healthy"
		if pct <= 10 {
			s, m = Warn, "low available memory"
		}
		if pct <= 5 {
			s, m = Fail, "critically low available memory"
		}
		return Result{Name: "memory", Status: s, Message: m, Details: map[string]any{"available_percent": pct, "swap_total_kb": vals["SwapTotal"], "swap_free_kb": vals["SwapFree"]}}
	})
	for _, path := range []string{"/", "/var/lib/containerd"} {
		p := path
		add("disk "+p, func(context.Context) Result {
			var st syscall.Statfs_t
			if e := syscall.Statfs(p, &st); e != nil {
				if p == "/var/lib/containerd" && os.IsNotExist(e) {
					return Result{Name: "disk " + p, Status: Warn, Message: "containerd data path absent"}
				}
				return failure("disk "+p, "filesystem unavailable")
			}
			total := st.Blocks * uint64(st.Bsize)
			used := (st.Blocks - st.Bavail) * uint64(st.Bsize)
			inodeUsed := uint64(0)
			if st.Files > st.Ffree {
				inodeUsed = st.Files - st.Ffree
			}
			if total == 0 || st.Files == 0 {
				return failure("disk "+p, "filesystem capacity unavailable")
			}
			bp, ip := ratio(used, total), ratio(inodeUsed, st.Files)
			s, m := threshold(bp, 80, 95)
			si, mi := threshold(ip, 80, 95)
			if si == Fail || (si == Warn && s == Pass) {
				s, m = si, "inode usage "+mi
			}
			return Result{Name: "disk " + p, Status: s, Message: m, Details: map[string]any{"used_percent": bp, "inode_used_percent": ip}}
		})
	}
	add("time synchronization", func(ctx context.Context) Result { return timeSync(ctx) })
	add("containerd service", func(ctx context.Context) Result { return serviceCheck(ctx, "containerd") })
	add("containerd socket", func(ctx context.Context) Result {
		c, e := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", "/run/containerd/containerd.sock")
		if e != nil {
			return failure("containerd socket", "containerd socket unavailable")
		}
		c.Close()
		return Result{Name: "containerd socket", Status: Pass, Message: "containerd socket reachable"}
	})
	add("containerd CRI", func(ctx context.Context) Result { return criCheck(ctx) })
	add("kubelet service", func(ctx context.Context) Result { return serviceCheck(ctx, "kubelet") })
}

func pressureCheck(path, category, name string, warn, fail float64) Result {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{Name: name, Status: Warn, Message: "pressure stall information unavailable"}
	}
	return parsePressure(strings.NewReader(string(b)), category, name, warn, fail)
}

func parsePressure(r io.Reader, category, name string, warn, fail float64) Result {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != category {
			continue
		}
		for _, field := range f[1:] {
			if strings.HasPrefix(field, "avg10=") {
				v, e := strconv.ParseFloat(strings.TrimPrefix(field, "avg10="), 64)
				if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
					return failure(name, "pressure data invalid")
				}
				s, msg := threshold(v, warn, fail)
				return Result{Name: name, Status: s, Message: msg, Details: map[string]any{"avg10": v}}
			}
		}
	}
	if scanner.Err() != nil {
		return failure(name, "pressure data unavailable")
	}
	return failure(name, "pressure data unavailable")
}

func readMeminfo() (map[string]uint64, error) {
	f, e := os.Open("/proc/meminfo")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return parseMeminfo(f)
}

func parseMeminfo(r io.Reader) (map[string]uint64, error) {
	out := map[string]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) >= 2 {
			v, e := strconv.ParseUint(p[1], 10, 64)
			if e != nil {
				return nil, fmt.Errorf("invalid meminfo value")
			}
			out[strings.TrimSuffix(p[0], ":")] = v
		}
	}
	if e := sc.Err(); e != nil {
		return nil, e
	}
	if _, ok := out["MemTotal"]; !ok {
		return nil, fmt.Errorf("MemTotal missing")
	}
	if _, ok := out["MemAvailable"]; !ok {
		return nil, fmt.Errorf("MemAvailable missing")
	}
	return out, nil
}
func threshold(v, warn, fail float64) (Status, string) {
	if v >= fail {
		return Fail, fmt.Sprintf("threshold exceeded (%.1f)", v)
	}
	if v >= warn {
		return Warn, fmt.Sprintf("elevated (%.1f)", v)
	}
	return Pass, "healthy"
}
func ratio(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}
func failure(name, msg string) Result {
	hint := "Inspect the affected subsystem's configuration and health; no corrective action was taken."
	switch {
	case strings.Contains(name, "memory") || strings.Contains(name, "CPU") || name == "cpu":
		hint = "Inspect host resource pressure and capacity before scheduling additional workloads."
	case strings.Contains(name, "disk"):
		hint = "Inspect filesystem space and inode use; avoid deleting data without identifying ownership."
	case strings.Contains(name, "time"):
		hint = "Inspect the configured time synchronization service and upstream sources."
	case strings.Contains(name, "containerd") || strings.Contains(name, "CRI"):
		hint = "Inspect containerd service, socket permissions, and CRI plugin health."
	case strings.Contains(name, "kubelet"):
		hint = "Inspect kubelet service health and its system journal."
	case strings.Contains(name, "DNS"):
		hint = "Inspect resolver configuration and upstream DNS reachability."
	case strings.Contains(name, "route") || strings.Contains(name, "gateway") || strings.Contains(name, "interface") || strings.Contains(name, "TCP") || strings.Contains(name, "API"):
		hint = "Inspect management interface, routing, firewall, and endpoint reachability."
	}
	return Result{Name: name, Status: Fail, Message: msg, Hint: hint}
}
