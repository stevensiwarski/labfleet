package fleetctl

import "testing"

func TestPodReadinessRequiresReadyContainer(t *testing.T) {
	var p kPod
	p.Status.Phase = "Running"
	p.Status.ContainerStatuses = []struct {
		Ready bool `json:"ready"`
	}{{Ready: true}}
	p.Status.Conditions = []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	}{{Type: "Ready", Status: "True"}}
	if !readyPod(p) {
		t.Fatal("ready pod rejected")
	}
	p.Status.ContainerStatuses[0].Ready = false
	if readyPod(p) {
		t.Fatal("unready container accepted")
	}
}
