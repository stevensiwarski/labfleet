package fleetctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func regressionFixture(t *testing.T, mutate func([]map[string]any, []map[string]any, []map[string]any)) (Config, Target, inspectRunner, func()) {
	t.Helper()
	rows, nodes, pods := make([]map[string]any, 6), make([]map[string]any, 6), []map[string]any{}
	vms := make([]VM, 6)
	for i := 0; i < 6; i++ {
		id, name, role := 900001+i, "labfleet-node"+string(rune('1'+i)), "worker"
		labels := map[string]string{"node-role.kubernetes.io/worker": ""}
		if i < 3 {
			role = "control-plane"
			labels = map[string]string{"node-role.kubernetes.io/control-plane": ""}
		}
		vms[i] = VM{ID: id, Name: name, Node: "pve", Role: role, Tags: []string{"labfleet", "disposable"}}
		rows[i] = map[string]any{"vmid": id, "name": name, "node": "pve", "type": "qemu", "status": "running", "tags": "labfleet;disposable"}
		nodes[i] = map[string]any{"metadata": map[string]any{"name": name, "labels": labels}, "status": map[string]any{"conditions": []map[string]string{{"type": "Ready", "status": "True"}}}}
		pods = append(pods,
			map[string]any{"metadata": map[string]any{"name": "cilium-" + name, "labels": map[string]string{"k8s-app": "cilium"}}, "spec": map[string]any{"nodeName": name}, "status": readyPodStatus()},
		)
	}
	pods = append(pods,
		map[string]any{"metadata": map[string]any{"name": "cilium-operator-abc"}, "status": readyPodStatus()},
		map[string]any{"metadata": map[string]any{"name": "coredns-abc", "labels": map[string]string{"k8s-app": "kube-dns"}}, "status": readyPodStatus()},
	)
	mutate(rows, nodes, pods)
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api2/json/cluster/resources" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tags": "labfleet;disposable", "protection": false}})
	}))
	t.Setenv("PROXMOX_VE_ENDPOINT", api.URL)
	t.Setenv("PROXMOX_VE_API_TOKEN", "test-token")
	t.Setenv("PROXMOX_VE_INSECURE", "true")
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kc, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Kubeconfig: kc, Kubectl: "true"}, Target{Kind: "cluster", VMs: vms}, inspectRunner{mustJSON(t, map[string]any{"items": nodes}), mustJSON(t, map[string]any{"items": pods})}, api.Close
}

func readyPodStatus() map[string]any {
	return map[string]any{"phase": "Running", "conditions": []map[string]string{{"type": "Ready", "status": "True"}}, "containerStatuses": []map[string]bool{{"ready": true}}}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestStatusRegressionMatrix(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func([]map[string]any, []map[string]any, []map[string]any)
		wantCheck string
	}{
		{"healthy", func(a, b, c []map[string]any) {}, "kubernetes_nodes"},
		{"missingVM", func(a, b, c []map[string]any) { a[0] = a[5] }, "vm_labfleet-node1"},
		{"stoppedVM", func(a, b, c []map[string]any) { a[0]["status"] = "stopped" }, "vm_labfleet-node1"},
		{"wrongrole", func(a, b, c []map[string]any) {
			b[0]["metadata"].(map[string]any)["labels"] = map[string]string{"node-role.kubernetes.io/worker": ""}
		}, "kubernetes_nodes"},
		{"ReadyFalse", func(a, b, c []map[string]any) {
			b[0]["status"].(map[string]any)["conditions"] = []map[string]string{{"type": "Ready", "status": "False"}}
		}, "kubernetes_nodes"},
		{"missingReady", func(a, b, c []map[string]any) { b[0]["status"].(map[string]any)["conditions"] = []map[string]string{} }, "kubernetes_nodes"},
		{"extraK8snode", func(a, b, c []map[string]any) {
			b[0] = map[string]any{"metadata": map[string]any{"name": "extra", "labels": map[string]string{"node-role.kubernetes.io/worker": ""}}, "status": map[string]any{"conditions": []map[string]string{{"type": "Ready", "status": "True"}}}}
		}, "kubernetes_nodes"},
		{"Ciliumoperatorunready", func(a, b, c []map[string]any) { c[len(c)-2]["status"] = map[string]any{"phase": "Pending"} }, "cilium_operators"},
		{"CoreDNSunready", func(a, b, c []map[string]any) { c[len(c)-1]["status"] = map[string]any{"phase": "Pending"} }, "coredns"},
		{"Ciliumwrongnodes", func(a, b, c []map[string]any) { c[0]["spec"].(map[string]any)["nodeName"] = "missing" }, "cilium"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, target, r, closeFn := regressionFixture(t, tt.mutate)
			defer closeFn()
			got := inspect(context.Background(), c, target, r, "status")
			if tt.name == "healthy" {
				if got.ExitCode != 0 {
					t.Fatalf("healthy baseline: %+v", got)
				}
				return
			}
			if got.ExitCode != 3 || got.Status != "degraded" {
				t.Fatalf("expected partial degraded status, got %+v", got)
			}
			found := false
			for _, ck := range got.Checks {
				if ck.Name == tt.wantCheck && ck.Status == "FAIL" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing failed check %q in %+v", tt.wantCheck, got.Checks)
			}
			if got.Data == nil {
				t.Fatal("expected partial status data")
			}
		})
	}
}
