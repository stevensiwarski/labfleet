package fleetctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

type lifecycleRunner struct{ calls int }

func (r *lifecycleRunner) Run(context.Context, runner.Command) (runner.Result, error) {
	r.calls++
	return runner.Result{}, nil
}

func clusterFixture(t *testing.T) (Config, Target, string) {
	t.Helper()
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "id_ed25519")
	if err := os.WriteFile(key, []byte("test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	ids := []int{930081, 930082, 930083, 930084, 930085, 930086}
	names := []string{"labfleet-cp-01", "labfleet-cp-02", "labfleet-cp-03", "labfleet-worker-01", "labfleet-worker-02", "labfleet-worker-03"}
	roles := []string{"control-plane", "control-plane", "control-plane", "worker", "worker", "worker"}
	vms := make([]VM, 6)
	fleet := make([]any, 6)
	for i := range ids {
		node := "pve1"
		tags := []string{"labfleet", "disposable", "issue8", roles[i]}
		vms[i] = VM{ID: ids[i], Name: names[i], Node: node, Role: roles[i], Tags: tags}
		fleet[i] = map[string]any{"vm_id": ids[i], "name": names[i], "role": roles[i], "tags": tags, "mac": fmt.Sprintf("02:00:00:00:00:%02x", i+1), "management_mac": fmt.Sprintf("02:00:00:00:01:%02x", i+1)}
	}
	varsPath := filepath.Join(root, "discovery.json")
	vars := map[string]any{"cluster_fleet": fleet, "cluster_private_dir": private, "cluster_ssh_private_key_file": key, "labfleet_proxmox_node": "pve1", "safety_validate_certs": true}
	b, _ := json.Marshal(vars)
	if err := os.WriteFile(varsPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{RepoRoot: root}
	target := Target{Kind: "cluster", VMs: vms, DiscoveryVars: varsPath, Inventory: filepath.Join(private, "inventory.json")}
	return c, target, private
}

func TestValidateClusterInputsScopeAndInventoryPath(t *testing.T) {
	c, target, _ := clusterFixture(t)
	if err := validateClusterInputs(c, target); err != nil {
		t.Fatalf("valid discovery input rejected: %v", err)
	}
	target.VMs[0].Node = "different-node"
	if err := validateClusterInputs(c, target); err == nil {
		t.Fatal("accepted target node mismatch")
	}
	target.VMs[0].Node = "pve1"
	target.Inventory = filepath.Join(c.RepoRoot, "other-inventory.json")
	if err := validateClusterInputs(c, target); err == nil {
		t.Fatal("accepted inventory path outside cluster private directory")
	}
}

func TestValidateDiscoveredInventoryUsesGeneratedRoleAndIdentityContract(t *testing.T) {
	_, target, private := clusterFixture(t)
	keyPath := filepath.Join(filepath.Dir(private), "id_ed25519")
	groups := map[string]any{}
	for group, spec := range map[string]struct {
		role       string
		start, end int
	}{"labfleet_control_plane": {"control-plane", 0, 3}, "labfleet_workers": {"worker", 3, 6}} {
		hosts := map[string]any{}
		for i := spec.start; i < spec.end; i++ {
			v := target.VMs[i]
			hosts[v.Name] = map[string]any{"ansible_host": fmt.Sprintf("192.0.2.%d", i+1), "ansible_user": "fleet", "kubernetes_node_ip": fmt.Sprintf("192.0.2.%d", i+1), "labfleet_vm_id": v.ID, "labfleet_expected_hostname": v.Name, "labfleet_kubernetes_role": spec.role, "labfleet_proxmox_node": v.Node, "labfleet_issue": "issue8", "labfleet_disposable": true, "ansible_ssh_private_key_file": keyPath, "ansible_ssh_common_args": "-o UserKnownHostsFile=" + filepath.Join(private, "known_hosts") + " -o HostKeyAlias=" + v.Name + " -o StrictHostKeyChecking=yes"}
		}
		groups[group] = map[string]any{"hosts": hosts}
	}
	doc := map[string]any{"all": map[string]any{"vars": map[string]any{"safety_validate_certs": "False", "kubernetes_control_plane_endpoint": "192.0.2.1:6443"}, "children": map[string]any{"labfleet_nodes": map[string]any{"children": groups}}}}
	path := filepath.Join(private, "inventory.json")
	write := func() {
		b, _ := json.Marshal(doc)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if err := validateDiscoveredInventory(path, target); err != nil {
		t.Fatalf("valid generated inventory rejected: %v", err)
	}
	all := doc["all"].(map[string]any)
	children := all["children"].(map[string]any)
	nodes := children["labfleet_nodes"].(map[string]any)
	roleGroups := nodes["children"].(map[string]any)
	cp := roleGroups["labfleet_control_plane"].(map[string]any)
	hosts := cp["hosts"].(map[string]any)
	delete(hosts, target.VMs[0].Name)
	cp["hosts"] = hosts
	roleGroups["labfleet_control_plane"] = cp
	nodes["children"] = roleGroups
	children["labfleet_nodes"] = nodes
	all["children"] = children
	doc["all"] = all
	write()
	if err := validateDiscoveredInventory(path, target); err == nil {
		t.Fatal("accepted inventory missing configured host")
	}
	for _, mutate := range []struct {
		name string
		fn   func(map[string]any)
	}{
		{"injected-connection", func(h map[string]any) { h["ansible_connection"] = "local" }},
		{"injected-global-var", func(h map[string]any) { h["ansible_become"] = true }},
		{"wrong-key", func(h map[string]any) { h["ansible_ssh_private_key_file"] = "/tmp/attacker-key" }},
		{"disabled-host-check", func(h map[string]any) { h["ansible_ssh_common_args"] = "-o StrictHostKeyChecking=no" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			freshGroups := map[string]any{}
			for group, spec := range map[string]struct {
				role       string
				start, end int
			}{"labfleet_control_plane": {"control-plane", 0, 3}, "labfleet_workers": {"worker", 3, 6}} {
				hosts := map[string]any{}
				for i := spec.start; i < spec.end; i++ {
					v := target.VMs[i]
					host := map[string]any{"ansible_host": fmt.Sprintf("192.0.2.%d", i+1), "ansible_user": "fleet", "kubernetes_node_ip": fmt.Sprintf("192.0.2.%d", i+1), "labfleet_vm_id": v.ID, "labfleet_expected_hostname": v.Name, "labfleet_kubernetes_role": spec.role, "labfleet_proxmox_node": v.Node, "labfleet_issue": "issue8", "labfleet_disposable": true, "ansible_ssh_private_key_file": keyPath, "ansible_ssh_common_args": "-o UserKnownHostsFile=" + filepath.Join(private, "known_hosts") + " -o HostKeyAlias=" + v.Name + " -o StrictHostKeyChecking=yes"}
					if i == 0 {
						mutate.fn(host)
					}
					hosts[v.Name] = host
				}
				freshGroups[group] = map[string]any{"hosts": hosts}
			}
			fresh := map[string]any{"all": map[string]any{"vars": map[string]any{"safety_validate_certs": "False", "kubernetes_control_plane_endpoint": "192.0.2.1:6443"}, "children": map[string]any{"labfleet_nodes": map[string]any{"children": freshGroups}}}}
			encoded, _ := json.Marshal(fresh)
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateDiscoveredInventory(path, target); err == nil {
				t.Fatal("accepted injected SSH inventory variable")
			}
		})
	}
}

func TestLifecycleRejectsUnacknowledgedPXEBeforeTools(t *testing.T) {
	r := &lifecycleRunner{}
	got := lifecycle(context.Background(), Config{}, Target{}, "provision", lifecycleOptions{Target: "test", Phase: "start", Apply: true}, r, nil, nil)
	if got.ExitCode != 4 || got.Error == nil || !strings.Contains(got.Error.Message, "PXE readiness") || r.calls != 0 {
		t.Fatalf("got=%+v calls=%d", got, r.calls)
	}
}

func TestConfirmTargetRequiresExactLine(t *testing.T) {
	if !confirmTarget(strings.NewReader("target-a\n"), "target-a") {
		t.Fatal("did not accept exact target")
	}
	if confirmTarget(strings.NewReader("target-b\n"), "target-a") {
		t.Fatal("accepted different target")
	}
	if confirmTarget(strings.NewReader("target-a-and-more\n"), "target-a") {
		t.Fatal("accepted target prefix")
	}
}
