package fleetctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveDiagnosticHostRejectsUnknownAndProtectedBeforeProvider(t *testing.T) {
	c := Config{Targets: map[string]Target{"cluster": {Kind: "cluster", VMs: []VM{{ID: 930081, Name: "labfleet-coding-agent", Node: "pve1"}}}}}
	for _, name := range []string{"missing", "labfleet-coding-agent"} {
		_, err := ResolveDiagnosticHost(context.Background(), c, "cluster", name)
		if !errors.Is(err, ErrUnsafeDiagnosticTarget) {
			t.Fatalf("%q: expected unsafe-target error, got %v", name, err)
		}
	}
}

func TestResolveDiagnosticHostAttestsAndReturnsPinnedHost(t *testing.T) {
	c, target, private := clusterFixture(t)
	vm := target.VMs[0]
	groups := map[string]any{}
	for group, bounds := range map[string][2]int{"labfleet_control_plane": {0, 3}, "labfleet_workers": {3, 6}} {
		hosts := map[string]any{}
		for i := bounds[0]; i < bounds[1]; i++ {
			v := target.VMs[i]
			hosts[v.Name] = map[string]any{"ansible_host": fmt.Sprintf("192.0.2.%d", i+1), "ansible_user": "fleet", "kubernetes_node_ip": fmt.Sprintf("192.0.2.%d", i+1), "labfleet_vm_id": v.ID, "labfleet_expected_hostname": v.Name, "labfleet_kubernetes_role": v.Role, "labfleet_proxmox_node": v.Node, "labfleet_issue": "issue8", "labfleet_disposable": true, "ansible_ssh_private_key_file": filepath.Join(c.RepoRoot, "id_ed25519"), "ansible_ssh_common_args": "-o UserKnownHostsFile=" + filepath.Join(private, "known_hosts") + " -o HostKeyAlias=" + v.Name + " -o StrictHostKeyChecking=yes"}
		}
		groups[group] = map[string]any{"hosts": hosts}
	}
	inventory := map[string]any{"all": map[string]any{"vars": map[string]any{"safety_validate_certs": true, "kubernetes_control_plane_endpoint": "192.0.2.1:6443"}, "children": map[string]any{"labfleet_nodes": map[string]any{"children": groups}}}}
	b, _ := json.Marshal(inventory)
	if err := os.WriteFile(target.Inventory, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "known_hosts"), []byte("pinned host key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Targets = map[string]Target{"cluster": target}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/cluster/resources" {
			fmt.Fprintf(w, `{"data":[{"vmid":%d,"name":%q,"node":%q,"type":"qemu","status":"running","tags":"labfleet;disposable;issue8;control-plane"}]}`, vm.ID, vm.Name, vm.Node)
			return
		}
		if r.URL.Path == fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/config", vm.Node, vm.ID) {
			fmt.Fprint(w, `{"data":{"tags":"labfleet;disposable;issue8;control-plane","protection":0}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("PROXMOX_VE_ENDPOINT", server.URL)
	t.Setenv("PROXMOX_VE_API_TOKEN", "test-token")
	t.Setenv("PROXMOX_VE_INSECURE", "true")
	host, err := ResolveDiagnosticHost(context.Background(), c, "cluster", vm.Name)
	if err != nil {
		t.Fatalf("resolve rejected valid host: %v", err)
	}
	if !reflect.DeepEqual(host.VM, vm) || host.Address != "192.0.2.1" || host.SSHKey != filepath.Join(c.RepoRoot, "id_ed25519") || host.KnownHosts != filepath.Join(private, "known_hosts") || host.APIServer != "192.0.2.1:6443" {
		t.Fatalf("unexpected resolved host: %+v", host)
	}
	if _, err := ResolveDiagnosticHost(context.Background(), c, "cluster", "LABFLEET-CP-01"); !errors.Is(err, ErrUnsafeDiagnosticTarget) {
		t.Fatalf("accepted non-exact node member: %v", err)
	}
}
