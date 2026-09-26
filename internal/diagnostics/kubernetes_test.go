package diagnostics

import (
	"encoding/json"
	"testing"

	"github.com/stevensiwarski/labfleet/internal/fleetctl"
)

func TestNodeConditions(t *testing.T) {
	vm := fleetctl.VM{Name: "labfleet-worker-01"}
	for _, tc := range []struct {
		name, condition, value string
		remove, fail           bool
	}{
		{name: "healthy"},
		{name: "ready false", condition: "Ready", value: "False", fail: true},
		{name: "ready unknown", condition: "Ready", value: "Unknown", fail: true},
		{name: "missing ready", condition: "Ready", remove: true, fail: true},
		{name: "memory", condition: "MemoryPressure", value: "True", fail: true},
		{name: "disk", condition: "DiskPressure", value: "True", fail: true},
		{name: "pid", condition: "PIDPressure", value: "True", fail: true},
		{name: "network", condition: "NetworkUnavailable", value: "True", fail: true},
		{name: "network not published", condition: "NetworkUnavailable", remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := []map[string]string{}
			for _, kind := range []string{"Ready", "MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"} {
				if kind == tc.condition && tc.remove {
					continue
				}
				value := "False"
				if kind == "Ready" {
					value = "True"
				}
				if kind == tc.condition {
					value = tc.value
				}
				cs = append(cs, map[string]string{"type": kind, "status": value})
			}
			b, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"metadata": map[string]string{"name": vm.Name}, "status": map[string]any{"conditions": cs}}}})
			results := NodeConditionResults(b, []fleetctl.VM{vm}, vm.Name)
			_, code := Aggregate(results)
			if (code == 3) != tc.fail {
				t.Fatalf("code %d results %+v", code, results)
			}
		})
	}
}

func TestNodeConditionsMalformedAndMissing(t *testing.T) {
	for _, raw := range []string{"invalid", `{"items":[]}`} {
		_, code := Aggregate(NodeConditionResults([]byte(raw), []fleetctl.VM{{Name: "labfleet-worker-01"}}, ""))
		if code != 3 {
			t.Fatal("missing evidence reported healthy")
		}
	}
}
