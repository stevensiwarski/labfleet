package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAggregate(t *testing.T) {
	for _, tc := range []struct {
		in     []Result
		status string
		code   int
	}{{nil, "unhealthy", 3}, {[]Result{{Status: Warn}}, "degraded", 1}, {[]Result{{Status: Warn}, {Status: Fail}}, "unhealthy", 3}, {[]Result{{Status: Status("bogus")}}, "unhealthy", 3}} {
		s, c := Aggregate(tc.in)
		if s != tc.status || c != tc.code {
			t.Fatalf("Aggregate = %s,%d", s, c)
		}
	}
}

func TestRunChecksPreservesInputOrder(t *testing.T) {
	checks := []Check{{Name: "slow", Run: func(ctx context.Context) Result {
		time.Sleep(20 * time.Millisecond)
		return Result{Status: Pass, Message: "ok"}
	}}, {Name: "fast", Run: func(context.Context) Result { return Result{Status: Warn, Message: "warning"} }}}
	r := RunChecks(context.Background(), "node", checks, 2, time.Second)
	if r.Results[0].Name != "slow" || r.Results[1].Name != "fast" || r.Status != "degraded" || r.ExitCode != 1 {
		t.Fatalf("unexpected report: %#v", r)
	}
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var decoded Report
	if e = json.Unmarshal(b, &decoded); e != nil || decoded.Results[1].Status != Warn {
		t.Fatalf("JSON roundtrip: %v %#v", e, decoded)
	}
}

func TestRunChecksTimeoutStopsStartingWork(t *testing.T) {
	var starts atomic.Int32
	release := make(chan struct{})
	checks := []Check{{Name: "stuck", Run: func(context.Context) Result { starts.Add(1); <-release; return Result{Status: Pass} }}, {Name: "never", Run: func(context.Context) Result { starts.Add(1); return Result{Status: Pass} }}}
	start := time.Now()
	r := RunChecks(context.Background(), "n", checks, 1, 15*time.Millisecond)
	if time.Since(start) > time.Second {
		t.Fatal("timeout did not bound RunChecks")
	}
	if starts.Load() != 1 || r.Results[0].Message != "check timed out" || r.Results[1].Message != "not run" {
		t.Fatalf("timeout result/starts: %d %#v", starts.Load(), r.Results)
	}
	close(release)
}

func TestRunChecksConcurrencyBound(t *testing.T) {
	var active, max atomic.Int32
	checks := make([]Check, 20)
	for i := range checks {
		checks[i] = Check{Name: fmt.Sprint(i), Run: func(context.Context) Result {
			n := active.Add(1)
			for {
				old := max.Load()
				if n <= old || max.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			return Result{Status: Pass}
		}}
	}
	r := RunChecks(context.Background(), "n", checks, 4, time.Second)
	if max.Load() > 4 || max.Load() < 2 || r.ExitCode != 0 {
		t.Fatalf("concurrency=%d report=%#v", max.Load(), r)
	}
}

func TestLinuxParsers(t *testing.T) {
	mem, err := parseMeminfo(strings.NewReader("MemTotal: 100 kB\nMemAvailable: 5 kB\nSwapTotal: 8 kB\nSwapFree: 3 kB\n"))
	if err != nil || mem["MemAvailable"] != 5 {
		t.Fatalf("meminfo=%v err=%v", mem, err)
	}
	for _, fixture := range []string{"MemTotal: 10 kB\n", "MemTotal: nope kB\nMemAvailable: 1 kB\n"} {
		if _, err := parseMeminfo(strings.NewReader(fixture)); err == nil {
			t.Errorf("accepted invalid meminfo %q", fixture)
		}
	}
	routeHead := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n"
	iface, gw, err := parseDefaultRoute(strings.NewReader(routeHead + "mgmt0 00000000 010200C0 0003 0 0 0 00000000 0 0 0\n"))
	if err != nil || iface != "mgmt0" || gw != "192.0.2.1" {
		t.Fatalf("route %s %s %v", iface, gw, err)
	}
	for _, row := range []string{"", "mgmt0 00000000 00000000 0003 0 0 0 00000000 0 0 0\n", "mgmt0 00000000 010200C0 0001 0 0 0 00000000 0 0 0\n", "mgmt0 00000000 010200C0 0003 0 0 0 FFFFFF00 0 0 0\n", "mgmt0 00000000 broken 0003 0 0 0 00000000 0 0 0\n"} {
		if _, _, err := parseDefaultRoute(strings.NewReader(routeHead + row)); err == nil {
			t.Errorf("accepted bad route row %q", row)
		}
	}
	if r := parsePressure(strings.NewReader("some avg10=NaN avg60=0 avg300=0 total=1\n"), "some", "CPU pressure", 20, 50); r.Status != Fail {
		t.Fatalf("invalid pressure: %#v", r)
	}
	if r := parsePressure(strings.NewReader("some avg10=50.1 avg60=0 avg300=0 total=1\n"), "some", "CPU pressure", 20, 50); r.Status != Fail {
		t.Fatalf("pressure threshold: %#v", r)
	}
}

func TestCRIPluginFixtures(t *testing.T) {
	for _, tc := range []struct {
		data string
		want bool
	}{{"io.containerd.cri.v1 runtime linux/amd64 ok\nio.containerd.cri.v1 images - ok", true}, {"io.containerd.cri.v1 runtime linux/amd64 ok", false}, {"io.containerd.cri.v1 images - ok", false}, {"io.containerd.cri.v1 runtime linux/amd64 error\nio.containerd.cri.v1 images - ok", false}, {"io.containerd.grpc.v1.cri cri linux/amd64 ok", true}} {
		if got := parseCRIPlugins(tc.data); got != tc.want {
			t.Errorf("CRI got %v want %v: %q", got, tc.want, tc.data)
		}
	}
}

func TestNetworkChecksWithLocalFixtures(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	oldLookup, oldDial := lookupIP, dialContext
	defer func() { lookupIP, dialContext = oldLookup, oldDial }()
	lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	dialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, a)
	}
	checks := []Check{}
	add := func(name string, fn func(context.Context) Result) {
		checks = append(checks, Check{Name: name, Run: fn})
	}
	addNetworkChecks(add, ProbeRequest{DNSName: "fixture.invalid", TCPEndpoints: []string{listener.Addr().String(), "127.0.0.1:1"}})
	// Exercise only DNS and endpoint checks: management state is intentionally not mocked.
	filtered := []Check{}
	for _, c := range checks {
		if c.Name == "DNS" || strings.HasPrefix(c.Name, "TCP endpoint") {
			filtered = append(filtered, c)
		}
	}
	r := RunChecks(context.Background(), "fixture", filtered, 2, time.Second)
	if r.Results[0].Status != Pass || r.Results[1].Status != Pass || r.Results[2].Status != Fail {
		t.Fatalf("local network checks: %#v", r.Results)
	}
}

func TestCPULoadNormalizedThreshold(t *testing.T) {
	for _, tc := range []struct {
		load float64
		want Status
	}{{3.99, Pass}, {4, Warn}, {7.99, Warn}, {8, Fail}} {
		got, _ := threshold(tc.load/4, 1, 2)
		if got != tc.want {
			t.Errorf("load %v => %s want %s", tc.load, got, tc.want)
		}
	}
}

func TestRunChecksCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	checks := []Check{{Name: "wait", Run: func(ctx context.Context) Result { <-ctx.Done(); return Result{Status: Fail} }}}
	done := make(chan Report, 1)
	go func() { done <- RunChecks(ctx, "n", checks, 1, time.Second) }()
	cancel()
	select {
	case r := <-done:
		if r.ExitCode != 130 {
			t.Fatalf("exit code %d", r.ExitCode)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation hung")
	}
}

func TestParsers(t *testing.T) {
	for _, input := range []string{"localhost:6443", "[::1]:443"} {
		if _, e := parseEndpoint(input); e != nil {
			t.Errorf("reject valid endpoint %q: %v", input, e)
		}
	}
	for _, input := range []string{"https://host:443", "user@host:443", "host:70000", "host"} {
		if _, e := parseEndpoint(input); e == nil {
			t.Errorf("accepted invalid endpoint %q", input)
		}
	}
	if s, _ := threshold(80, 80, 95); s != Warn {
		t.Fatalf("threshold boundary: %s", s)
	}
}
