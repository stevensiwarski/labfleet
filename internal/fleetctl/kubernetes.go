package fleetctl

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

type kNode struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}
type kList struct {
	Items []kNode `json:"items"`
}
type kPod struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			Ready bool `json:"ready"`
		} `json:"containerStatuses"`
	} `json:"status"`
}
type podList struct {
	Items []kPod `json:"items"`
}

func readyNode(n kNode) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}
func nodeRole(n kNode) string {
	if _, ok := n.Metadata.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return "control-plane"
	}
	if _, ok := n.Metadata.Labels["node-role.kubernetes.io/master"]; ok {
		return "control-plane"
	}
	if _, ok := n.Metadata.Labels["node-role.kubernetes.io/worker"]; ok {
		return "worker"
	}
	return "unknown"
}
func readyPod(p kPod) bool {
	if p.Status.Phase != "Running" || len(p.Status.ContainerStatuses) == 0 {
		return false
	}
	readyCondition := false
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" {
			readyCondition = c.Status == "True"
		}
	}
	if !readyCondition {
		return false
	}
	for _, c := range p.Status.ContainerStatuses {
		if !c.Ready {
			return false
		}
	}
	return true
}
func inspectKubernetes(ctx context.Context, c Config, t Target, r runner.Runner, command string) envelope {
	checks := []check{{Name: "config", Status: "PASS", Message: "configuration loaded"}}
	add := func(name, status, msg string) {
		checks = append(checks, check{Name: name, Status: status, Message: msg})
	}
	finish := func(data any) envelope {
		status, code := "ok", 0
		for _, x := range checks {
			if x.Status == "FAIL" {
				status, code = "degraded", 3
				break
			}
		}
		return envelope{Command: command, Status: status, ExitCode: code, Data: data, Checks: checks}
	}
	var nodes []Node
	p, e := newProviderFromEnv()
	if e != nil {
		return envelope{Command: command, Status: "error", ExitCode: 2, Error: &problem{Classification: "config", Message: e.Error()}}
	}
	nodes, e = p.inventory(ctx)
	if e != nil {
		add("proxmox", "FAIL", "provider inventory unavailable")
	} else {
		add("proxmox", "PASS", "provider inventory available")
		if command == "nodes" {
			return finish(NodesData{nodes})
		}
	}
	statusData := StatusData{ManagedVMs: len(nodes), Cilium: "unknown", CiliumOperators: "unknown", CoreDNS: "unknown"}
	if command != "nodes" {
		statusData.ExpectedVMs = len(t.VMs)
		inventory := map[int]Node{}
		for _, n := range nodes {
			inventory[n.VMID] = n
		}
		for _, v := range t.VMs {
			if v.Role == "control-plane" || v.Role == "controlplane" {
				statusData.ControlPlanesExpected++
			} else if v.Role == "worker" {
				statusData.WorkersExpected++
			}
			n, ok := inventory[v.ID]
			if !ok || n.Name != v.Name || n.Node != v.Node || (t.Kind == "cluster" && n.State != "running") {
				add("vm_"+v.Name, "FAIL", "expected VM is missing, stopped, or has mismatched identity")
			} else {
				n.Role = v.Role
				inventory[v.ID] = n
				if err := p.attest(ctx, v); err != nil {
					add("vm_"+v.Name, "FAIL", "VM ownership attestation failed")
				} else {
					add("vm_"+v.Name, "PASS", "VM identity and ownership attested")
				}
			}
		}
		provisioner, err := p.provisioner(ctx)
		if err != nil {
			add("provisioner", "WARN", "provisioner state unavailable")
		} else {
			statusData.Provisioner = provisioner
		}
	}
	if command == "validate" {
		validateTools(c, t, add)
	}
	if command == "nodes" {
		return finish(NodesData{nodes})
	}
	if t.Kind != "cluster" {
		add("kubernetes", "WARN", "Kubernetes health is not applicable to this VM-only target")
		return finish(statusData)
	}
	if c.Kubeconfig == "" {
		add("kubernetes", "FAIL", "Kubernetes health is unknown: no kubeconfig is configured")
		return finish(statusData)
	}
	st, err := os.Lstat(c.Kubeconfig)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		add("kubeconfig", "FAIL", "kubeconfig must be a private regular file")
		return finish(statusData)
	}
	if _, err = exec.LookPath(c.Kubectl); err != nil {
		add("kubectl", "FAIL", "kubectl is unavailable")
		return finish(statusData)
	}
	env := append(os.Environ(), "KUBECONFIG="+filepath.Clean(c.Kubeconfig))
	nr, ne := r.Run(ctx, runner.Command{Name: c.Kubectl, Args: []string{"get", "nodes", "-o", "json"}, Env: env, Timeout: 30 * time.Second})
	var nl kList
	if ne != nil || json.Unmarshal(nr.Stdout, &nl) != nil {
		add("kubernetes_api", "FAIL", "Kubernetes API is unreachable")
		return finish(statusData)
	}
	statusData.APIReachable = true
	expected := map[string]VM{}
	for _, v := range t.VMs {
		expected[v.Name] = v
	}
	seen := map[string]bool{}
	topologyOK := true
	for _, n := range nl.Items {
		v, ok := expected[n.Metadata.Name]
		if !ok || seen[n.Metadata.Name] {
			topologyOK = false
			continue
		}
		seen[n.Metadata.Name] = true
		wantRole := v.Role
		if wantRole == "controlplane" {
			wantRole = "control-plane"
		}
		if nodeRole(n) != wantRole || !readyNode(n) {
			topologyOK = false
		}
		if wantRole == "control-plane" {
			if readyNode(n) && nodeRole(n) == wantRole {
				statusData.ControlPlanesReady++
			}
		} else if wantRole == "worker" {
			if readyNode(n) && nodeRole(n) == wantRole {
				statusData.WorkersReady++
			}
		}
	}
	if len(seen) != len(expected) {
		topologyOK = false
	}
	if topologyOK {
		add("kubernetes_nodes", "PASS", "expected nodes, roles, and readiness match")
	} else {
		add("kubernetes_nodes", "FAIL", "Kubernetes node names, roles, or readiness do not match configured topology")
	}
	pr, pe := r.Run(ctx, runner.Command{Name: c.Kubectl, Args: []string{"get", "pods", "-n", "kube-system", "-o", "json"}, Env: env, Timeout: 30 * time.Second})
	var pl podList
	if pe != nil || json.Unmarshal(pr.Stdout, &pl) != nil {
		statusData.Cilium = "unknown"
		statusData.CiliumOperators = "unknown"
		statusData.CoreDNS = "unknown"
		add("kubernetes_pods", "FAIL", "system pod inventory unavailable")
		return finish(statusData)
	}
	wanted := map[string]bool{}
	for name := range expected {
		wanted[name] = true
	}
	ciliumNodes := map[string]bool{}
	ciliumOK := true
	operators, operatorReady := 0, true
	dns, dnsReady := 0, true
	for _, pod := range pl.Items {
		app := pod.Metadata.Labels["k8s-app"]
		if app == "cilium" {
			if !wanted[pod.Spec.NodeName] || ciliumNodes[pod.Spec.NodeName] || !readyPod(pod) {
				ciliumOK = false
			}
			ciliumNodes[pod.Spec.NodeName] = true
		}
		if strings.HasPrefix(pod.Metadata.Name, "cilium-operator") {
			operators++
			if !readyPod(pod) {
				operatorReady = false
			}
		}
		if app == "kube-dns" {
			dns++
			if !readyPod(pod) {
				dnsReady = false
			}
		}
	}
	statusData.Cilium = "PASS"
	if !ciliumOK || len(ciliumNodes) != len(expected) || len(expected) != 6 {
		statusData.Cilium = "FAIL"
	}
	statusData.CiliumOperators = "PASS"
	if operators == 0 || !operatorReady {
		statusData.CiliumOperators = "FAIL"
	}
	statusData.CoreDNS = "PASS"
	if dns == 0 || !dnsReady {
		statusData.CoreDNS = "FAIL"
	}
	for _, item := range []struct{ name, value string }{{"cilium", statusData.Cilium}, {"cilium_operators", statusData.CiliumOperators}, {"coredns", statusData.CoreDNS}} {
		add(item.name, item.value, "system component readiness")
	}
	return finish(statusData)
}

func validateTools(c Config, t Target, add func(string, string, string)) {
	for _, tool := range []struct{ name, path string }{{"tofu", c.Tofu}, {"ansible_playbook", c.Ansible}, {"kubectl", c.Kubectl}} {
		if tool.path == "" {
			add(tool.name, "FAIL", "tool is not configured")
		} else if _, err := exec.LookPath(tool.path); err != nil {
			add(tool.name, "FAIL", "tool is unavailable")
		} else {
			add(tool.name, "PASS", "tool is available")
		}
	}
	root := filepath.Join(c.RepoRoot, t.TofuRoot)
	if filepath.IsAbs(t.TofuRoot) {
		root = t.TofuRoot
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		add("tofu_root", "FAIL", "target OpenTofu directory is unavailable")
	} else {
		add("tofu_root", "PASS", "target OpenTofu directory is available")
	}
	state := filepath.Join(root, "terraform.tfstate")
	if st, err = os.Stat(state); err != nil || !st.Mode().IsRegular() {
		add("tofu_state", "WARN", "no local state file; live VM attestation remains authoritative")
	} else {
		add("tofu_state", "PASS", "local state file is present")
	}
}
