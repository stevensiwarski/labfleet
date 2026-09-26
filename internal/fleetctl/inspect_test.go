package fleetctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

func TestRoleRequiresExplicitLabel(t *testing.T) {
	var n kNode
	n.Metadata.Labels = map[string]string{}
	if got := nodeRole(n); got != "unknown" {
		t.Fatalf("role=%q", got)
	}
	n.Metadata.Labels["node-role.kubernetes.io/worker"] = ""
	if got := nodeRole(n); got != "worker" {
		t.Fatalf("role=%q", got)
	}
}

type inspectRunner struct{ nodes, pods []byte }

func (r inspectRunner) Run(_ context.Context, c runner.Command) (runner.Result, error) {
	if len(c.Args) > 1 && c.Args[1] == "nodes" {
		return runner.Result{Stdout: r.nodes}, nil
	}
	return runner.Result{Stdout: r.pods}, nil
}

func TestInspectStatusRejectsMissingManagedVMAndExtraNode(t *testing.T) {
	rows := make([]map[string]any, 6)
	vms := make([]VM, 6)
	knodes := make([]map[string]any, 6)
	for i := range rows {
		id := 900001 + i
		name := string(rune('a' + i))
		role := "worker"
		labels := map[string]string{"node-role.kubernetes.io/worker": ""}
		if i < 3 {
			role = "control-plane"
			labels = map[string]string{"node-role.kubernetes.io/control-plane": ""}
		}
		vms[i] = VM{ID: id, Name: name, Node: "pve", Role: role, Tags: []string{"labfleet", "disposable"}}
		rows[i] = map[string]any{"vmid": id, "name": name, "node": "pve", "type": "qemu", "status": "running", "tags": "labfleet;disposable"}
		knodes[i] = map[string]any{"metadata": map[string]any{"name": name, "labels": labels}, "status": map[string]any{"conditions": []map[string]string{{"type": "Ready", "status": "True"}}}}
	}
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/cluster/resources" {
			json.NewEncoder(w).Encode(map[string]any{"data": rows})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tags": "labfleet;disposable", "protection": false}})
	}))
	defer api.Close()
	t.Setenv("PROXMOX_VE_ENDPOINT", api.URL)
	t.Setenv("PROXMOX_VE_API_TOKEN", "test-token")
	t.Setenv("PROXMOX_VE_INSECURE", "true")
	kc := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kc, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	knodes = append(knodes, map[string]any{"metadata": map[string]any{"name": "unexpected", "labels": map[string]string{"node-role.kubernetes.io/worker": ""}}, "status": map[string]any{"conditions": []map[string]string{{"type": "Ready", "status": "True"}}}})
	nb, _ := json.Marshal(map[string]any{"items": knodes})
	pods, _ := json.Marshal(map[string]any{"items": []any{}})
	c := Config{Kubeconfig: kc, Kubectl: "true"}
	target := Target{Kind: "cluster", VMs: vms}
	got := inspect(context.Background(), c, target, inspectRunner{nb, pods}, "status")
	if got.ExitCode != 3 || got.Status != "degraded" {
		t.Fatalf("unexpected topology accepted: %+v", got)
	}
}

func TestStatusWithoutKubeconfigIsDegradedUnknownNotHealthy(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer api.Close()
	t.Setenv("PROXMOX_VE_ENDPOINT", api.URL)
	t.Setenv("PROXMOX_VE_API_TOKEN", "test-token")
	t.Setenv("PROXMOX_VE_INSECURE", "true")
	got := inspect(context.Background(), Config{}, Target{Kind: "cluster"}, inspectRunner{}, "status")
	if got.ExitCode == 0 || got.Status == "ok" {
		t.Fatalf("missing Kubernetes health reported healthy: %+v", got)
	}
	found := false
	for _, c := range got.Checks {
		if c.Name == "kubernetes" && c.Status == "FAIL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing failing health check: %+v", got.Checks)
	}
}
