package diagnostics

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

type ProbeRequest struct {
	Target         string   `json:"target"`
	Mode           string   `json:"mode"`
	APIServer      string   `json:"api_server,omitempty"`
	DNSName        string   `json:"dns_name,omitempty"`
	TCPEndpoints   []string `json:"tcp_endpoints,omitempty"`
	ManagementIP   string   `json:"management_ip,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Concurrency    int      `json:"concurrency,omitempty"`
}

func RunProbe(ctx context.Context, req ProbeRequest) Report {
	if req.Target == "" || (req.Mode != "node" && req.Mode != "network" && req.Mode != "check") {
		return errorReport(req.Target, "invalid target or mode", 2)
	}
	host, err := os.Hostname()
	if err != nil || host != req.Target || !strings.HasPrefix(strings.ToLower(host), "labfleet-") || strings.Contains(strings.ToLower(host), "provisioner") || strings.Contains(strings.ToLower(host), "coding-agent") {
		return errorReport(req.Target, "host identity is not an eligible LabFleet node", 4)
	}
	if req.APIServer != "" {
		if _, err := parseEndpoint(req.APIServer); err != nil {
			return errorReport(req.Target, "invalid API server endpoint", 2)
		}
	}
	if len(req.TCPEndpoints) > 8 {
		return errorReport(req.Target, "too many TCP endpoints", 2)
	}
	for _, e := range req.TCPEndpoints {
		if _, err := parseEndpoint(e); err != nil {
			return errorReport(req.Target, "invalid TCP endpoint", 2)
		}
	}
	if req.DNSName == "" {
		req.DNSName = "pkgs.k8s.io"
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = 10
	}
	if req.TimeoutSeconds < 1 || req.TimeoutSeconds > 60 {
		return errorReport(req.Target, "timeout_seconds must be between 1 and 60", 2)
	}
	if req.Concurrency == 0 {
		req.Concurrency = 4
	}
	if req.Concurrency < 1 || req.Concurrency > 8 {
		return errorReport(req.Target, "concurrency must be between 1 and 8", 2)
	}
	checks := make([]Check, 0)
	add := func(name string, run func(context.Context) Result) {
		checks = append(checks, Check{Name: name, Run: run})
	}
	if req.Mode != "network" {
		addHostChecks(add, req)
	}
	addNetworkChecks(add, req)
	if req.Mode == "network" { // network only excludes host lifecycle checks
		// addNetworkChecks contains only network checks.
	}
	return RunChecks(ctx, req.Target, checks, req.Concurrency, time.Duration(req.TimeoutSeconds)*time.Second)
}

func errorReport(target, msg string, code int) Report {
	return Report{Target: target, Status: "unhealthy", ExitCode: code, Results: []Result{{Name: "request", Status: Fail, Message: msg, Target: target}}}
}

func parseEndpoint(s string) (string, error) {
	h, p, err := net.SplitHostPort(s)
	if err != nil || h == "" {
		return "", fmt.Errorf("invalid endpoint")
	}
	port, err := net.LookupPort("tcp", p)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid endpoint")
	}
	if strings.ContainsAny(h, "/@?#") {
		return "", fmt.Errorf("invalid endpoint")
	}
	return net.JoinHostPort(h, p), nil
}
