package fleetctl

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

// ErrUnsafeDiagnosticTarget marks an unknown, protected, or unattested target.
var ErrUnsafeDiagnosticTarget = errors.New("unsafe diagnostic target")

type DiagnosticHost struct {
	VM         VM
	Address    string
	SSHKey     string
	KnownHosts string
	APIServer  string
}

// ResolveDiagnosticHost resolves one configured cluster node without opening SSH.
func ResolveDiagnosticHost(ctx context.Context, c Config, targetName, nodeName string) (DiagnosticHost, error) {
	t, ok := c.Targets[targetName]
	if !ok || t.Kind != "cluster" || nodeName == "" {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	var vm *VM
	for i := range t.VMs {
		if t.VMs[i].Name == nodeName {
			vm = &t.VMs[i]
			break
		}
	}
	if vm == nil || protectedName(vm.Name) || vm.ID == 100 || vm.ID == protectedVMID {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	if err := validateClusterInputs(c, t); err != nil {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	p, err := newProviderFromEnv()
	if err != nil {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	if err = p.attest(ctx, *vm); err != nil {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	if err = validateDiscoveredInventory(t.Inventory, t); err != nil {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	invPath := t.Inventory
	if !filepath.IsAbs(invPath) {
		invPath = filepath.Join(c.RepoRoot, invPath)
	}
	b, err := os.ReadFile(invPath)
	if err != nil {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	type diagnosticInventoryHost struct {
		Address string `json:"ansible_host"`
		NodeIP  string `json:"kubernetes_node_ip"`
		Key     string `json:"ansible_ssh_private_key_file"`
	}
	var doc struct {
		All struct {
			Vars struct {
				Endpoint string `json:"kubernetes_control_plane_endpoint"`
			} `json:"vars"`
			Children map[string]struct {
				Children map[string]struct {
					Hosts map[string]diagnosticInventoryHost `json:"hosts"`
				} `json:"children"`
			} `json:"children"`
		} `json:"all"`
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	children := doc.All.Children["labfleet_nodes"].Children
	group := "labfleet_workers"
	if vm.Role == "control-plane" {
		group = "labfleet_control_plane"
	}
	host := children[group].Hosts[vm.Name]
	address, nodeIP, key, endpoint := host.Address, host.NodeIP, host.Key, doc.All.Vars.Endpoint
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil || address != nodeIP {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	if st, e := os.Lstat(key); e != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	known := filepath.Join(filepath.Dir(invPath), "known_hosts")
	st, e := os.Lstat(known)
	if e != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0600 {
		return DiagnosticHost{}, ErrUnsafeDiagnosticTarget
	}
	return DiagnosticHost{VM: *vm, Address: address, SSHKey: key, KnownHosts: known, APIServer: endpoint}, nil
}

type DiagnosticCheck struct {
	Name    string
	Status  string
	Message string
}
type DiagnosticInspection struct {
	Status   string
	ExitCode int
	Data     StatusData
	Checks   []DiagnosticCheck
}

// InspectForDiagnostics adapts the established, read-only cluster status inspection.
func InspectForDiagnostics(ctx context.Context, c Config, targetName string, r runner.Runner) (DiagnosticInspection, error) {
	t, ok := c.Targets[targetName]
	if !ok || t.Kind != "cluster" {
		return DiagnosticInspection{}, ErrUnsafeDiagnosticTarget
	}
	e := inspect(ctx, c, t, r, "status")
	if e.Error != nil {
		return DiagnosticInspection{}, errors.New("cluster inspection failed")
	}
	data, ok := e.Data.(StatusData)
	if !ok {
		return DiagnosticInspection{}, errors.New("cluster inspection returned unexpected data")
	}
	out := DiagnosticInspection{Status: e.Status, ExitCode: e.ExitCode, Data: data}
	for _, x := range e.Checks {
		out.Checks = append(out.Checks, DiagnosticCheck{Name: x.Name, Status: x.Status, Message: x.Message})
	}
	return out, nil
}
