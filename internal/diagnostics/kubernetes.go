package diagnostics

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/stevensiwarski/labfleet/internal/fleetctl"
	"github.com/stevensiwarski/labfleet/internal/runner"
)

// KubernetesResults reuses fleetctl's topology/component health definitions and
// adds individual Node conditions. Exactly two bounded read-only tasks run;
// result order is independent of their completion order.
func KubernetesResults(ctx context.Context, c fleetctl.Config, scope, node string, r runner.Runner) []Result {
	target := scope
	if node != "" {
		target = node
	}
	var shared, conditions []Result
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		start := time.Now()
		inspection, err := fleetctl.InspectForDiagnostics(ctx, c, scope, r)
		if err != nil {
			shared = []Result{{Name: "kubernetes.inspection", Status: Fail, Target: target, Message: "cluster inspection unavailable", Hint: "Check provider credentials, ownership configuration, and private kubeconfig.", DurationMS: time.Since(start).Milliseconds()}}
			return
		}
		for _, check := range inspection.Checks {
			shared = append(shared, Result{Name: "cluster." + check.Name, Status: Status(check.Status), Target: scope, Message: check.Message, DurationMS: time.Since(start).Milliseconds(), Hint: inspectionHint(check.Name)})
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		start := time.Now()
		fail := func(message string) {
			conditions = []Result{{Name: "kubernetes.conditions", Status: Fail, Target: target, Message: message, DurationMS: time.Since(start).Milliseconds(), Hint: "Check Kubernetes API access and the configured node identities."}}
		}
		st, err := os.Lstat(c.Kubeconfig)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
			fail("private kubeconfig is unavailable")
			return
		}
		result, err := r.Run(ctx, runner.Command{Name: c.Kubectl, Args: []string{"get", "nodes", "-o", "json"}, Env: append(os.Environ(), "KUBECONFIG="+c.Kubeconfig), Timeout: 30 * time.Second})
		if err != nil {
			fail("node-condition query failed")
			return
		}
		t, ok := c.Targets[scope]
		if !ok {
			fail("unknown configured scope")
			return
		}
		conditions = NodeConditionResults(result.Stdout, t.VMs, node)
		for i := range conditions {
			conditions[i].DurationMS = time.Since(start).Milliseconds()
		}
	}()
	<-done
	<-done
	return append(shared, conditions...)
}

func inspectionHint(name string) string {
	switch name {
	case "proxmox":
		return "Check management API reachability and configured ownership credentials."
	case "kubernetes_nodes":
		return "Inspect kubelet/runtime health and the reported per-node conditions."
	case "cilium", "cilium_operators":
		return "Inspect Cilium pod readiness and node-local networking; do not restart services blindly."
	case "coredns":
		return "Inspect CoreDNS pod readiness, scheduling, and upstream resolver connectivity."
	default:
		return "Compare the evidence with the expected fleet topology and configuration."
	}
}

// NodeConditionResults is pure diagnostic logic over Kubernetes Node JSON.
func NodeConditionResults(raw []byte, expected []fleetctl.VM, selected string) []Result {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct{ Type, Status, Reason string }
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return []Result{{Name: "kubernetes.conditions", Target: selected, Status: Fail, Message: "malformed Kubernetes node response", Hint: "Check API response compatibility."}}
	}
	wanted := append([]fleetctl.VM(nil), expected...)
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].Name < wanted[j].Name })
	results := []Result{}
	for _, vm := range wanted {
		if selected != "" && vm.Name != selected {
			continue
		}
		seen := 0
		conditions := map[string]string{}
		for _, n := range list.Items {
			if n.Metadata.Name == vm.Name {
				seen++
				for _, c := range n.Status.Conditions {
					conditions[c.Type] = c.Status
				}
			}
		}
		if seen != 1 {
			results = append(results, Result{Name: "kubernetes.node", Target: vm.Name, Status: Fail, Message: "expected node missing or ambiguous", Hint: "Check kubelet registration and expected node identity."})
			continue
		}
		for _, kind := range []string{"Ready", "MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"} {
			value, exists := conditions[kind]
			status, message := Pass, kind+"="+value
			if !exists {
				status, message = Fail, kind+" condition is missing"
				if kind == "NetworkUnavailable" {
					status, message = Pass, "NetworkUnavailable condition not published (optional)"
				}
			} else if kind == "Ready" && value != "True" || kind != "Ready" && value != "False" {
				status = Fail
			}
			results = append(results, Result{Name: "kubernetes." + kind, Target: vm.Name, Status: status, Message: message, Hint: conditionHint(kind)})
		}
	}
	if len(results) == 0 {
		return []Result{{Name: "kubernetes.node", Target: selected, Status: Fail, Message: "target is not in the expected node set", Hint: "Select an explicitly configured fleet node."}}
	}
	return results
}

func conditionHint(kind string) string {
	switch kind {
	case "Ready":
		return "Inspect kubelet, containerd, and node-to-API connectivity."
	case "MemoryPressure":
		return "Inspect available memory and workload requests/limits."
	case "DiskPressure":
		return "Inspect filesystem space, inode availability, and runtime storage."
	case "PIDPressure":
		return "Inspect PID limits and process count without exposing process contents."
	default:
		return "Inspect Cilium readiness, management routing, and node network configuration."
	}
}
