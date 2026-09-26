// Package fleetctl implements the reusable fleetctl operator command core.
package fleetctl

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"
	"unsafe"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

// Build metadata may be injected with -ldflags; development builds make no
// claim about a release, commit, or build timestamp.
var Version = "dev"
var Commit = "unknown"
var BuildTime = "unknown"

type envelope struct {
	Command  string   `json:"command"`
	Status   string   `json:"status"`
	ExitCode int      `json:"exit_code"`
	Data     any      `json:"data,omitempty"`
	Checks   []check  `json:"checks,omitempty"`
	Error    *problem `json:"error,omitempty"`
}
type check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type problem struct {
	Classification string `json:"classification"`
	Message        string `json:"message"`
	ToolExitCode   *int   `json:"tool_exit_code,omitempty"`
}
type lifecycleOptions struct {
	Target, Phase                           string
	Plan, Apply, Yes, PXEReady, Interactive bool
}

func Run(ctx context.Context, args []string, in io.Reader, out, errout io.Writer) int {
	return run(ctx, args, in, out, errout, runner.Exec{})
}

func run(ctx context.Context, args []string, in io.Reader, out, errout io.Writer, r runner.Runner) int {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	output := "text"
	// Discover the requested format even when a preceding option is invalid.
	for i, a := range args {
		if a == "--output=json" || (a == "--output" && i+1 < len(args) && args[i+1] == "json") {
			output = "json"
		}
	}
	fail := func(code int, class, msg string) int {
		emit(out, errout, output, envelope{Command: command, Status: "error", ExitCode: code, Error: &problem{Classification: class, Message: msg}})
		return code
	}
	if ctx == nil {
		return fail(2, "usage", "context is required")
	}
	if command == "" || command == "help" || command == "--help" || command == "-h" {
		emit(out, errout, output, envelope{Command: "help", Status: "ok", Data: helpText})
		return 0
	}
	switch command {
	case "version", "nodes", "status", "validate", "provision", "destroy":
	default:
		return fail(2, "usage", "unknown command; use fleetctl help")
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configPath := f.String("config", os.Getenv("FLEETCTL_CONFIG"), "private configuration file")
	format := f.String("output", output, "text or json")
	target := f.String("target", "", "configured target name")
	phase := f.String("phase", "infrastructure", "infrastructure, start, or bootstrap")
	plan := f.Bool("plan", false, "review only; do not execute changes")
	apply := f.Bool("apply", false, "execute the checked provision phase")
	yes := f.Bool("yes", false, "deliberate non-interactive destroy confirmation")
	pxe := f.Bool("pxe-ready", false, "acknowledge existing isolated PXE has been prepared")
	err := f.Parse(args[1:])
	if err == flag.ErrHelp {
		emit(out, errout, output, envelope{Command: command, Status: "ok", Data: helpText})
		return 0
	}
	if err != nil || f.NArg() != 0 {
		return fail(2, "usage", "invalid command options; use fleetctl help")
	}
	if *format != "text" && *format != "json" {
		return fail(2, "usage", "output must be text or json")
	}
	output = *format
	used := map[string]bool{}
	f.Visit(func(v *flag.Flag) { used[v.Name] = true })
	life := command == "provision" || command == "destroy"
	if (!life && (used["phase"] || used["plan"] || used["apply"] || used["yes"] || used["pxe-ready"])) ||
		(command != "destroy" && used["yes"]) || (command != "provision" && (used["phase"] || used["apply"] || used["pxe-ready"])) ||
		(*plan && (*apply || *yes)) || (*pxe && *phase != "start") {
		return fail(2, "usage", "options conflict or are not applicable to this command")
	}
	if command == "version" {
		emit(out, errout, output, envelope{Command: command, Status: "ok", Data: map[string]string{"version": Version, "commit": Commit, "build_time": BuildTime}})
		return 0
	}
	if life && *target == "" {
		return fail(4, "unsafe_target", "an explicit --target is required for lifecycle operations")
	}
	if *configPath == "" {
		return fail(2, "config", "config path required (--config or FLEETCTL_CONFIG)")
	}
	c, err := ReadConfig(*configPath)
	if err != nil {
		return fail(2, "config", err.Error())
	}
	if *target == "" {
		*target = c.DefaultTarget
	}
	t, ok := c.Targets[*target]
	if !ok {
		return fail(4, "unsafe_target", "target is not a configured LabFleet scope; no operation performed")
	}
	if ctx.Err() != nil {
		return fail(130, "canceled", "operation canceled")
	}
	var e envelope
	if life {
		if *phase != "infrastructure" && *phase != "start" && *phase != "bootstrap" {
			return fail(2, "usage", "unsupported provision phase")
		}
		o := lifecycleOptions{Target: *target, Phase: *phase, Plan: *plan, Apply: *apply, Yes: *yes, PXEReady: *pxe, Interactive: isTerminal(in)}
		if command == "provision" && !o.Apply {
			o.Plan = true
		}
		e = lifecycle(ctx, c, t, command, o, r, in, errout)
	} else {
		e = inspect(ctx, c, t, r, command)
	}
	if ctx.Err() != nil {
		return fail(130, "canceled", "operation canceled; inspect state before resuming any lifecycle phase")
	}
	emit(out, errout, output, e)
	return e.ExitCode
}

func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	var term syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&term)))
	return errno == 0
}

const helpText = `fleetctl version|nodes|status|validate [--config PATH] [--target NAME] [--output text|json]
fleetctl provision --target NAME [--phase infrastructure|start|bootstrap] [--plan|--apply] [--pxe-ready]
fleetctl destroy --target NAME [--plan|--yes]
fleetctl tofu-check -plan PATH

Provision defaults to a checked plan. Starting guests requires external isolated
PXE preparation and --pxe-ready with --apply. Bootstrap uses existing Ansible plays.
Destroy displays a checked plan, then requires a terminal and the exact target
name unless --yes is supplied. --plan never applies changes. See docs/fleetctl.md.`

func emit(out, errout io.Writer, output string, e envelope) {
	if output == "json" {
		_ = json.NewEncoder(out).Encode(e)
		return
	}
	fmt.Fprintf(out, "%s: %s (exit %d)\n", e.Command, e.Status, e.ExitCode)
	switch d := e.Data.(type) {
	case NodesData:
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVMID\tROLE\tSTATE\tOWNED\tK8S")
		for _, n := range d.Nodes {
			ready := "-"
			if n.Ready != nil {
				ready = "NotReady"
				if *n.Ready {
					ready = "Ready"
				}
			}
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%t\t%s\n", n.Name, n.VMID, n.Role, n.State, n.Owned, ready)
		}
		_ = w.Flush()
	case StatusData:
		fmt.Fprintf(out, "Managed VMs:       %d\nExpected VMs:      %d\nControl planes:    %d/%d Ready\nWorkers:           %d/%d Ready\nKubernetes API:    %t\nCilium:            %s\nCilium operators:  %s\nCoreDNS:           %s\n", d.ManagedVMs, d.ExpectedVMs, d.ControlPlanesReady, d.ControlPlanesExpected, d.WorkersReady, d.WorkersExpected, d.APIReachable, d.Cilium, d.CiliumOperators, d.CoreDNS)
		if d.Provisioner != nil {
			fmt.Fprintf(out, "Provisioner VM:   %s (PXE service state not queried)\n", d.Provisioner.State)
		}
	case string:
		fmt.Fprintln(out, d)
	default:
		if d != nil {
			b, _ := json.MarshalIndent(d, "", "  ")
			fmt.Fprintln(out, string(b))
		}
	}
	for _, c := range e.Checks {
		fmt.Fprintf(out, "%s %-22s %s\n", strings.ToUpper(c.Status), c.Name, c.Message)
	}
	if e.Error != nil {
		fmt.Fprintf(errout, "%s: %s\n", e.Error.Classification, e.Error.Message)
	}
}
