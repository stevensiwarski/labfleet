package tofusafety

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer(t *testing.T, data any, redirect bool) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirect {
			http.Redirect(w, r, "https://other.invalid/", http.StatusFound)
			return
		}
		if r.URL.Path != "/api2/json/cluster/resources" || r.URL.Query().Get("type") != "vm" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if r.Header.Get("Authorization") != "PVEAPIToken=user@pve!id=secret" {
			t.Error("missing token")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}
func planJSON(changes any, extra string) []byte {
	return []byte(`{"format_version":"1.2","terraform_version":"1.12.6","errored":false,"resource_changes":` + mustJSON(changes) + extra + `}`)
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func rc(action string, before, after any, unknown any) map[string]any {
	return map[string]any{"mode": "managed", "type": resourceType, "address": resourceAddress, "change": map[string]any{"actions": []string{action}, "before": before, "after": after, "after_unknown": unknown}}
}
func TestPlanActionsAndIdentity(t *testing.T) {
	owned := map[string]any{"type": "qemu", "vmid": 900001, "name": "labfleet-node", "node": "pve", "tags": "other;labfleet"}
	base := map[string]any{"id": "900001", "vm_id": 900001, "name": "labfleet-node", "node_name": "pve", "tags": []any{"labfleet"}}
	tests := []struct {
		name      string
		changes   any
		inventory any
		extra     string
		state     any
		want      int
		fail      bool
	}{
		{"valid create", []any{rc("create", nil, map[string]any{"vm_id": 900002, "name": "labfleet-new", "node_name": "pve", "tags": []any{"labfleet"}}, map[string]any{"disk": true, "ipv4_addresses": true})}, []any{owned}, "", nil, 1, false},
		{"unrelated data", []any{map[string]any{"mode": "data", "type": "foo", "address": "data.foo.x", "change": map[string]any{"actions": []string{"read"}}}}, []any{owned}, "", nil, 0, false},
		{"LXC collision", []any{rc("create", nil, map[string]any{"vm_id": 900005, "name": "labfleet-new", "node_name": "pve", "tags": []any{"labfleet"}}, nil)}, []any{owned, map[string]any{"type": "lxc", "vmid": 900005, "name": "container", "node": "pve"}}, "", nil, 0, true},
		{"valid update", []any{rc("update", base, map[string]any{"id": "900001", "vm_id": 900001, "name": "labfleet-node", "node_name": "pve", "tags": []any{"labfleet", "extra"}}, map[string]any{"disk": true})}, []any{owned}, "", nil, 1, false},
		{"valid delete", []any{rc("delete", base, nil, nil)}, []any{owned}, "", nil, 1, false},
		{"provider ID mismatch delete", []any{rc("delete", map[string]any{"id": "100", "vm_id": 900001, "name": "labfleet-node", "node_name": "pve", "tags": []any{"labfleet"}}, nil, nil)}, []any{owned}, "", nil, 0, true},
		{"provider ID mismatch state", []any{rc("no-op", base, base, nil)}, []any{owned}, "", stateVM(map[string]any{"id": "100", "vm_id": 900001, "name": "labfleet-node", "node_name": "pve"}), 0, true},
		{"create beside untagged bootstrap", []any{rc("create", nil, base, nil)}, []any{map[string]any{"type": "qemu", "vmid": 100, "name": "bootstrap", "node": "pve"}}, "", nil, 1, false},
		{"delete untagged", []any{rc("delete", base, nil, nil)}, []any{map[string]any{"type": "qemu", "vmid": 900001, "name": "labfleet-node", "node": "pve"}}, "", nil, 0, true},
		{"no-op without state still checked", []any{rc("no-op", base, base, nil)}, []any{map[string]any{"type": "qemu", "vmid": 900001, "name": "labfleet-node", "node": "pve"}}, "", nil, 0, true},
		{"bootstrap ID rejected", []any{rc("delete", map[string]any{"vm_id": 100, "name": "labfleet-bootstrap", "node_name": "pve"}, nil, nil)}, []any{map[string]any{"type": "qemu", "vmid": 100, "name": "labfleet-bootstrap", "node": "pve", "tags": "labfleet"}}, "", nil, 0, true},
		{"no-op owned", []any{rc("no-op", base, base, nil)}, []any{owned}, "", stateVM(base), 0, false},
		{"no-op untagged", []any{rc("no-op", base, base, nil)}, []any{map[string]any{"type": "qemu", "vmid": 900001, "name": "labfleet-node", "node": "pve", "tags": "other"}}, "", stateVM(base), 0, true},
		{"no-op identity mismatch", []any{rc("no-op", base, base, nil)}, []any{owned}, "", stateVM(map[string]any{"vm_id": 900001, "name": "labfleet-renamed", "node_name": "pve"}), 0, true},
		{"tag stripping", []any{rc("update", base, map[string]any{"id": "900001", "vm_id": 900001, "name": "labfleet-node", "node_name": "pve", "tags": []any{"other"}}, nil)}, []any{owned}, "", nil, 0, true},
		{"import", []any{func() any {
			x := rc("no-op", base, base, nil)
			x["change"].(map[string]any)["importing"] = map[string]any{"id": "x"}
			return x
		}()}, []any{owned}, "", nil, 0, true},
		{"replacement", []any{map[string]any{"mode": "managed", "type": resourceType, "address": resourceAddress, "change": map[string]any{"actions": []string{"delete", "create"}}}}, []any{owned}, "", nil, 0, true},
		{"duplicate address", []any{rc("no-op", base, base, nil), rc("no-op", base, base, nil)}, []any{owned}, "", nil, 0, true},
		{"unknown identity", []any{rc("create", nil, map[string]any{"vm_id": 900002, "name": "labfleet-new", "node_name": "pve", "tags": []any{"labfleet"}}, map[string]any{"name": true})}, []any{owned}, "", nil, 0, true},
		{"nonidentity computed values", []any{rc("create", nil, map[string]any{"vm_id": 900002, "name": "labfleet-new", "node_name": "pve", "tags": []any{"labfleet"}}, map[string]any{"disk": true, "ipv4_addresses": true})}, []any{owned}, "", nil, 1, false},
		{"nested unsupported state", []any{rc("no-op", base, base, nil)}, []any{owned}, "", nestedState(), 0, true},
		{"valid state child module", []any{rc("no-op", base, base, nil)}, []any{owned}, "", childState(base), 0, false},
		{"deferred", []any{}, []any{}, `,"deferred_changes":[{}]`, nil, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := testServer(t, tt.inventory, false)
			defer srv.Close()
			b := planJSON(tt.changes, tt.extra)
			if tt.state != nil {
				var obj map[string]any
				_ = json.Unmarshal(b, &obj)
				obj["prior_state"] = tt.state
				b, _ = json.Marshal(obj)
			}
			got, err := Check(b, srv.URL, "user@pve!id=secret", true)
			if (err != nil) != tt.fail || (!tt.fail && got != tt.want) {
				t.Fatalf("got %d err=%v", got, err)
			}
		})
	}
}
func stateVM(v map[string]any) any {
	return map[string]any{"values": map[string]any{"root_module": map[string]any{"resources": []any{map[string]any{"mode": "managed", "type": resourceType, "address": resourceAddress, "values": v}}}}}
}
func nestedState() any {
	return map[string]any{"values": map[string]any{"root_module": map[string]any{"child_modules": []any{map[string]any{"resources": []any{map[string]any{"mode": "managed", "type": "foo", "address": "foo.x", "values": map[string]any{}}}}}}}}
}
func childState(v map[string]any) any {
	return map[string]any{"values": map[string]any{"root_module": map[string]any{"child_modules": []any{map[string]any{"resources": []any{map[string]any{"mode": "managed", "type": resourceType, "address": resourceAddress, "values": v}}}}}}}
}
func TestMalformedPlanAndInventory(t *testing.T) {
	for _, b := range [][]byte{[]byte(`{}`), []byte(`{"format_version":"1.2","terraform_version":"1.12.6","errored":false,"resource_changes":[]} garbage`), []byte(`{"format_version":"1.2","terraform_version":"1.12.6","errored":true,"resource_changes":[]}`), []byte(`{"format_version":"1.2","terraform_version":"1.12.6","errored":false,"complete":false,"resource_changes":[]}`)} {
		s := testServer(t, []any{}, false)
		if _, e := Check(b, s.URL, "token", true); e == nil {
			t.Fatal("malformed/incomplete plan accepted")
		}
		s.Close()
	}
	for _, inventory := range []any{nil, []any{map[string]any{"type": "qemu", "vmid": "oops", "name": "x", "node": "pve", "tags": ""}}, []any{map[string]any{"type": "qemu", "vmid": 900001, "name": "x", "node": "pve", "tags": ""}, map[string]any{"type": "lxc", "vmid": 900001, "name": "x", "node": "pve"}}} {
		s := testServer(t, inventory, false)
		if _, e := Check(planJSON([]any{}, ""), s.URL, "user@pve!id=secret", true); e == nil {
			t.Fatal("malformed inventory accepted")
		}
		s.Close()
	}
}
func TestEndpointAndRedirect(t *testing.T) {
	for _, endpoint := range []string{"http://localhost", "https://user:pass@example.invalid", "https://example.invalid/path"} {
		if _, e := Check(planJSON([]any{}, ""), endpoint, "token", false); e == nil {
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	s := testServer(t, []any{}, true)
	defer s.Close()
	if _, e := Check(planJSON([]any{}, ""), s.URL, "user@pve!id=secret", true); e == nil {
		t.Fatal("redirect accepted")
	}
}
