package diagnostics

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/stevensiwarski/labfleet/internal/fleetctl"
	"github.com/stevensiwarski/labfleet/internal/runner"
)

// Run executes the node-doctor CLI and returns its process exit status.
func Run(ctx context.Context, args []string, in io.Reader, out, errout io.Writer) int {
	return runCLI(ctx, args, in, out, errout, runner.Exec{})
}

func runCLI(ctx context.Context, args []string, in io.Reader, out, errout io.Writer, r runner.Runner) int {
	format := "text"
	for i, a := range args {
		if a == "--output=json" || (a == "--output" && i+1 < len(args) && args[i+1] == "json") {
			format = "json"
		}
	}
	reject := func(code int, message string) int {
		report := failed("", "request", message)
		report.ExitCode = code
		return writeReport(out, format, report)
	}
	if len(args) == 0 {
		return reject(2, "expected a diagnostic command; use node-doctor help")
	}
	command := args[0]
	if command == "help" || command == "-h" || command == "--help" {
		usage(out)
		return 0
	}
	if command != "node" && command != "network" && command != "check" && command != "cluster" && command != "probe" {
		return reject(2, "unknown command; use node-doctor help")
	}
	if command == "probe" {
		return runProbeCLI(ctx, args[1:], in, out, errout)
	}
	fs := flag.NewFlagSet("node-doctor "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var config, scope, dns string
	var concurrency, timeout, overall int
	var endpoints listFlag
	fs.StringVar(&config, "config", os.Getenv("FLEETCTL_CONFIG"), "private fleet configuration")
	fs.StringVar(&scope, "scope", "", "target scope")
	fs.StringVar(&format, "output", format, "text or json")
	fs.IntVar(&concurrency, "concurrency", 4, "maximum probe concurrency (1-8)")
	fs.IntVar(&timeout, "timeout-seconds", 10, "per-check timeout (1-60 seconds)")
	fs.IntVar(&overall, "overall-timeout-seconds", 120, "overall timeout (1-600 seconds)")
	fs.StringVar(&dns, "dns-name", "pkgs.k8s.io", "DNS name to probe")
	fs.Var(&endpoints, "tcp", "additional TCP endpoint host:port (repeatable)")
	flagArgs := args[1:]
	leading := ""
	if command != "cluster" && len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[0], "-") {
		leading, flagArgs = flagArgs[0], flagArgs[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		if err == flag.ErrHelp {
			usage(out)
			return 0
		}
		return reject(2, "invalid diagnostic options")
	}
	pos := fs.Args()
	node := ""
	if command != "cluster" {
		if leading != "" && len(pos) == 0 {
			node = leading
		} else if leading == "" && len(pos) == 1 {
			node = pos[0]
		} else {
			return reject(2, "expected exactly one node name")
		}
	} else if len(pos) != 0 {
		return reject(2, "cluster takes no node name")
	}
	if format != "text" && format != "json" || concurrency < 1 || concurrency > 8 || timeout < 1 || timeout > 60 || overall < 1 || overall > 600 || len(endpoints) > 8 {
		return reject(2, "invalid option value")
	}
	for _, endpoint := range endpoints {
		if _, err := parseEndpoint(endpoint); err != nil {
			return reject(2, "invalid TCP endpoint")
		}
	}
	if config == "" {
		return reject(2, "configuration path is required")
	}
	c, err := fleetctl.ReadConfig(config)
	if err != nil {
		return reject(2, "could not read private fleet configuration")
	}
	if scope == "" {
		scope = c.DefaultTarget
	}
	if scope == "" {
		return reject(2, "target scope is required")
	}
	target, ok := c.Targets[scope]
	if !ok || target.Kind != "cluster" {
		return reject(4, "scope is not a configured LabFleet cluster")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(overall)*time.Second)
	defer cancel()
	var report Report
	if command == "cluster" {
		results := KubernetesResults(ctx, c, scope, "", r)
		status, code := Aggregate(results)
		report = Report{Target: scope, Status: status, ExitCode: code, Results: results}
	} else {
		host, e := fleetctl.ResolveDiagnosticHost(ctx, c, scope, node)
		if e != nil {
			report = failed(node, "target.resolve", "could not resolve an eligible configured node")
			report.ExitCode = 4
		} else {
			mode := command
			if mode == "check" {
				mode = "node"
			}
			req := ProbeRequest{Target: host.VM.Name, Mode: mode, APIServer: host.APIServer, ManagementIP: host.Address, DNSName: dns, TCPEndpoints: endpoints, TimeoutSeconds: timeout, Concurrency: concurrency}
			report = RunRemoteProbe(ctx, host, req, remoteBinaryPath, r)
			if command != "network" && report.ExitCode != 4 && report.ExitCode != 2 && report.ExitCode != 130 {
				report.Results = append(report.Results, KubernetesResults(ctx, c, scope, node, r)...)
				report.Status, report.ExitCode = Aggregate(report.Results)
			}
		}
	}
	if ctx.Err() != nil {
		report.ExitCode = 130
		report.Status = "canceled"
	}
	return writeReport(out, format, report)
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }
func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: node-doctor <node|network|check> NAME [options] | node-doctor cluster [options]\nOptions: --config PATH --scope TARGET --output text|json --concurrency N --timeout-seconds N --overall-timeout-seconds N --dns-name NAME --tcp HOST:PORT\nRemote nodes must have the Go probe installed at /usr/local/bin/node-doctor. The internal probe command accepts JSON on stdin.")
}
func failed(target, name, msg string) Report {
	return Report{Target: target, Status: "unhealthy", ExitCode: 3, Results: []Result{{Name: name, Target: target, Status: Fail, Message: msg}}}
}
func writeReport(w io.Writer, format string, report Report) int {
	if format == "json" {
		if err := json.NewEncoder(w).Encode(report); err != nil {
			return 3
		}
	} else {
		fmt.Fprintf(w, "TARGET %s  STATUS %s\n", report.Target, report.Status)
		fmt.Fprintf(w, "CHECK\tRESULT\tDETAILS\tHINT\tDURATION\n")
		for _, r := range report.Results {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%dms\n", r.Name, r.Status, r.Message, r.Hint, r.DurationMS)
		}
	}
	return report.ExitCode
}

func runProbeCLI(ctx context.Context, args []string, in io.Reader, out, errout io.Writer) int {
	if len(args) != 0 {
		return writeReport(out, "json", errorReport("", "probe accepts JSON on stdin only", 2))
	}
	dec := json.NewDecoder(io.LimitReader(in, 1<<20))
	dec.DisallowUnknownFields()
	var req ProbeRequest
	if err := dec.Decode(&req); err != nil {
		return writeReport(out, "json", errorReport(req.Target, "invalid probe request", 2))
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return writeReport(out, "json", errorReport(req.Target, "invalid probe request", 2))
	}
	return writeReport(out, "json", RunProbe(ctx, req))
}
