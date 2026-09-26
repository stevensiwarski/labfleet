package fleetctl

import (
	"context"
	"github.com/stevensiwarski/labfleet/internal/runner"
)

type NodesData struct {
	Nodes []Node `json:"nodes"`
}
type StatusData struct {
	ManagedVMs            int    `json:"managed_vms"`
	ExpectedVMs           int    `json:"expected_vms"`
	ControlPlanesReady    int    `json:"control_planes_ready"`
	ControlPlanesExpected int    `json:"control_planes_expected"`
	WorkersReady          int    `json:"workers_ready"`
	WorkersExpected       int    `json:"workers_expected"`
	APIReachable          bool   `json:"kubernetes_api_reachable"`
	Cilium                string `json:"cilium"`
	CiliumOperators       string `json:"cilium_operators"`
	CoreDNS               string `json:"coredns"`
	Provisioner           *Node  `json:"provisioner,omitempty"`
}

// inspect is the read-only command implementation shared by nodes/status/validate.
func inspect(ctx context.Context, c Config, target Target, r runner.Runner, command string) envelope {
	return inspectKubernetes(ctx, c, target, r, command)
}
