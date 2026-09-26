package fleetctl

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/stevensiwarski/labfleet/internal/runner"
	"github.com/stevensiwarski/labfleet/internal/tofusafety"
)

func lifecycle(ctx context.Context, c Config, t Target, command string, o lifecycleOptions, r runner.Runner, in io.Reader, progress io.Writer) envelope {
	if progress == nil {
		progress = io.Discard
	}
	if ctx == nil || r == nil {
		return lifecycleError(command, "internal", "lifecycle dependencies unavailable", 4, nil)
	}
	if command != "provision" && command != "destroy" {
		return lifecycleError(command, "usage", "unsupported lifecycle command", 2, nil)
	}
	if o.Phase == "" {
		o.Phase = "infrastructure"
	}
	if command == "destroy" {
		o.Phase = "infrastructure"
	}
	if o.Phase != "infrastructure" && o.Phase != "start" && o.Phase != "bootstrap" {
		return lifecycleError(command, "usage", "unsupported phase", 2, nil)
	}
	if o.Apply && o.Plan {
		return lifecycleError(command, "usage", "--plan and --apply are mutually exclusive", 2, nil)
	}
	if err := validateExecutionEnvironment(); err != nil {
		return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
	}
	if command == "provision" && needsPXEAck(o.Phase, o.Apply) && !o.PXEReady {
		return lifecycleError(command, "guard_refused", "PXE readiness must be explicitly verified before applying start", 4, nil)
	}
	root, err := lifecycleRoot(c, t)
	if err != nil {
		return lifecycleError(command, "config", err.Error(), 2, nil)
	}
	vars := t.VarsFile
	if !filepath.IsAbs(vars) {
		vars = filepath.Join(c.RepoRoot, vars)
	}
	st, e := os.Stat(vars)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return lifecycleError(command, "config", "target vars file must be private and available", 2, nil)
	}
	if command == "destroy" && len(t.VMs) == 0 {
		return lifecycleError(command, "unsafe_target", "destroy requires a complete configured VM target", 4, nil)
	}
	wdir, err := os.MkdirTemp(c.WorkDir, "fleetctl-")
	if err != nil {
		return lifecycleError(command, "config", "cannot create private lifecycle workspace", 2, nil)
	}
	if err = os.Chmod(wdir, 0700); err != nil {
		return lifecycleError(command, "config", "cannot secure lifecycle workspace", 2, nil)
	}
	planPath := filepath.Join(wdir, "plan.tfplan")
	tofu := c.Tofu
	if tofu == "" {
		tofu = "tofu"
	}
	timeout := 30 * time.Minute
	if c.TimeoutSeconds > 0 {
		timeout = time.Duration(c.TimeoutSeconds) * time.Second
	}
	run := func(label string, args []string) (runner.Result, error) {
		fmt.Fprintln(progress, label)
		return r.Run(ctx, runner.Command{Name: tofu, Args: args, Dir: root, Timeout: timeout})
	}
	if _, err = run("tofu init", []string{"init", "-input=false", "-lockfile=readonly"}); err != nil {
		return runnerFailure(command, err)
	}
	args := []string{"plan", "-input=false", "-no-color", "-var-file=" + vars, "-out=" + planPath}
	if command == "destroy" {
		args = append(args, "-destroy")
	}
	if o.Phase == "start" {
		args = append(args, "-var=started=true")
	}
	_, err = run("tofu plan", args)
	if err != nil {
		return runnerFailure(command, err)
	}
	if err = os.Chmod(planPath, 0600); err != nil {
		return lifecycleError(command, "execution", "cannot secure saved plan", 1, nil)
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return lifecycleError(command, "execution", "cannot read saved plan", 1, nil)
	}
	planDigest := sha256.Sum256(planBytes)
	shown, err := run("tofu show", []string{"show", "-json", planPath})
	if err != nil {
		return runnerFailure(command, err)
	}
	if err = os.WriteFile(filepath.Join(wdir, "plan.json"), shown.Stdout, 0600); err != nil {
		return lifecycleError(command, "execution", "cannot preserve private plan inspection artifact", 1, nil)
	}
	if err = validatePlanProvider(shown.Stdout, t.Kind); err != nil {
		return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
	}
	actions, err := validateTargetPlan(shown.Stdout, t, o.Phase, command == "destroy")
	if err != nil {
		return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
	}
	if command == "destroy" {
		for _, a := range actions {
			fmt.Fprintf(progress, "planned delete: %s (%d)\n", a.Name, a.VMID)
		}
	}
	count, err := tofusafety.Check(shown.Stdout, os.Getenv("PROXMOX_VE_ENDPOINT"), normalizedToken(), insecureProvider())
	if err != nil {
		return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
	}
	if command == "provision" && o.Phase == "infrastructure" {
		for _, a := range actions {
			if a.Action != "create" && a.Action != "no-op" {
				return lifecycleError(command, "guard_refused", "infrastructure phase permits only create or no-op", 4, nil)
			}
		}
	}
	if lifecyclePlanOnly(command, o) {
		data := map[string]any{"target": o.Target, "phase": o.Phase, "plan_only": true, "changes": count, "actions": actions, "plan_artifact": planPath, "workspace": wdir}
		if o.Phase == "bootstrap" {
			data["workflow"] = []string{"discover-cluster.yml", "bootstrap-cluster.yml", "validate-cluster.yml"}
			data["message"] = "Bootstrap workflow displayed; no Ansible changes executed"
		}
		return envelope{Command: command, Status: "planned", ExitCode: 0, Data: data}
	}
	if o.Phase == "start" && !o.PXEReady {
		return lifecycleError(command, "guard_refused", "start requires explicit PXE readiness acknowledgement", 4, nil)
	}
	if destroyNeedsConfirmation(command, o.Yes) {
		if !o.Interactive {
			return lifecycleError(command, "guard_refused", "destroy requires --yes in non-interactive mode", 4, nil)
		}
		if !confirmDestroyContext(ctx, in, progress, o.Target) {
			return lifecycleError(command, "guard_refused", "destroy confirmation did not match target", 4, nil)
		}
	}
	if !binaryPlanMatches(planPath, planDigest) {
		return lifecycleError(command, "guard_refused", "saved binary plan changed after confirmation", 4, nil)
	}
	if command == "provision" && o.Phase == "bootstrap" {
		if t.Kind != "cluster" {
			return lifecycleError(command, "guard_refused", "bootstrap is only supported for cluster targets", 4, nil)
		}
		if err = validateClusterInputs(c, t); err != nil {
			return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
		}
		p, pErr := newProviderFromEnv()
		if pErr != nil {
			return lifecycleError(command, "guard_refused", pErr.Error(), 4, nil)
		}
		live, liveErr := p.inventory(ctx)
		if liveErr != nil {
			return lifecycleError(command, "guard_refused", liveErr.Error(), 4, nil)
		}
		for _, v := range t.VMs {
			if err = p.attest(ctx, v); err != nil {
				return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
			}
			found := false
			for _, n := range live {
				if n.VMID == v.ID && n.Name == v.Name && n.Node == v.Node && n.State == "running" {
					found = true
					break
				}
			}
			if !found {
				return lifecycleError(command, "guard_refused", "cluster VM is not running as configured", 4, nil)
			}
		}
		ansible := c.Ansible
		if ansible == "" {
			ansible = "ansible-playbook"
		}
		inv := t.Inventory
		if !filepath.IsAbs(inv) {
			inv = filepath.Join(c.RepoRoot, inv)
		}
		varsPath := t.DiscoveryVars
		if !filepath.IsAbs(varsPath) {
			varsPath = filepath.Join(c.RepoRoot, varsPath)
		}
		ansibleDir := filepath.Join(c.RepoRoot, "ansible")
		if _, err = r.Run(ctx, runner.Command{Name: ansible, Args: []string{"-i", "localhost,", "playbooks/discover-cluster.yml", "-e", "@" + varsPath}, Dir: ansibleDir, Timeout: timeout}); err != nil {
			return runnerFailure(command, err)
		}
		if err = validateDiscoveredInventory(inv, t); err != nil {
			return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
		}
		for _, playbook := range []string{"bootstrap-cluster.yml", "validate-cluster.yml"} {
			if _, err = r.Run(ctx, runner.Command{Name: ansible, Args: []string{"-i", inv, "playbooks/" + playbook}, Dir: ansibleDir, Timeout: timeout}); err != nil {
				return runnerFailure(command, err)
			}
		}
		return envelope{Command: command, Status: "ok", ExitCode: 0, Data: map[string]any{"target": o.Target, "phase": o.Phase, "bootstrapped": true}}
	}
	// Repeat the live ownership validation directly before applying the exact saved plan.
	shownAgain, err := run("tofu show", []string{"show", "-json", planPath})
	if err != nil {
		return runnerFailure(command, err)
	}
	if !samePlanJSON(shownAgain.Stdout, shown.Stdout) || !binaryPlanMatches(planPath, planDigest) {
		return lifecycleError(command, "guard_refused", "saved plan changed after validation", 4, nil)
	}
	if _, err = tofusafety.Check(shownAgain.Stdout, os.Getenv("PROXMOX_VE_ENDPOINT"), normalizedToken(), insecureProvider()); err != nil {
		return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
	}
	provider, err := newProviderFromEnv()
	if err != nil {
		return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
	}
	for _, action := range actions {
		if action.Action == "create" {
			continue
		}
		var v VM
		for _, configured := range t.VMs {
			if configured.ID == action.VMID {
				v = configured
				break
			}
		}
		if err = provider.attest(ctx, v); err != nil {
			return lifecycleError(command, "guard_refused", err.Error(), 4, nil)
		}
	}
	if !binaryPlanMatches(planPath, planDigest) {
		return lifecycleError(command, "guard_refused", "saved plan changed before apply", 4, nil)
	}
	if _, err = run("tofu apply", []string{"apply", "-input=false", "-no-color", planPath}); err != nil {
		return runnerFailure(command, err)
	}
	return envelope{Command: command, Status: "ok", ExitCode: 0, Data: map[string]any{"target": o.Target, "phase": o.Phase, "applied": true, "changes": count}}
}

func samePlanJSON(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

func normalizedToken() string {
	x := os.Getenv("PROXMOX_VE_API_TOKEN")
	return strings.TrimPrefix(x, "PVEAPIToken=")
}

func lifecyclePlanOnly(command string, o lifecycleOptions) bool {
	return o.Plan || (command != "destroy" && !o.Apply)
}
func needsPXEAck(phase string, apply bool) bool              { return phase == "start" && apply }
func destroyNeedsConfirmation(command string, yes bool) bool { return command == "destroy" && !yes }
func binaryPlanMatches(path string, digest [32]byte) bool {
	b, err := os.ReadFile(path)
	return err == nil && sha256.Sum256(b) == digest
}

func validateClusterInputs(c Config, t Target) error {
	if t.Kind != "cluster" || len(t.VMs) != 6 || t.DiscoveryVars == "" || t.Inventory == "" {
		return errors.New("cluster discovery and inventory paths are required")
	}
	vars := t.DiscoveryVars
	if !filepath.IsAbs(vars) {
		vars = filepath.Join(c.RepoRoot, vars)
	}
	st, err := os.Lstat(vars)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return errors.New("discovery vars must be a private regular file")
	}
	b, err := os.ReadFile(vars)
	if err != nil {
		return errors.New("cannot read discovery vars")
	}
	var values map[string]any
	if json.Unmarshal(b, &values) != nil {
		return errors.New("discovery vars must be JSON")
	}
	for key := range values {
		if key != "cluster_fleet" && key != "cluster_private_dir" && key != "cluster_ssh_private_key_file" && key != "labfleet_proxmox_node" && key != "safety_validate_certs" {
			return errors.New("discovery vars contain unsupported keys")
		}
	}
	if len(values) != 5 {
		return errors.New("discovery vars are incomplete")
	}
	privateDir, _ := values["cluster_private_dir"].(string)
	keyPath, _ := values["cluster_ssh_private_key_file"].(string)
	node, _ := values["labfleet_proxmox_node"].(string)
	if !filepath.IsAbs(privateDir) || !filepath.IsAbs(keyPath) || node == "" || strings.ContainsAny(privateDir+keyPath, " \t\r\n") {
		return errors.New("discovery paths or Proxmox node are invalid")
	}
	if st, err := os.Lstat(privateDir); err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
			return errors.New("cluster private directory must be a private non-symlink directory")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot inspect cluster private directory")
	} else {
		parent, parentErr := os.Lstat(filepath.Dir(privateDir))
		if parentErr != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 || parent.Mode().Perm()&0077 != 0 {
			return errors.New("parent of cluster private directory must be private")
		}
	}
	inventory := t.Inventory
	if !filepath.IsAbs(inventory) {
		inventory = filepath.Join(c.RepoRoot, inventory)
	}
	if filepath.Clean(inventory) != filepath.Join(filepath.Clean(privateDir), "inventory.json") {
		return errors.New("inventory path must be cluster_private_dir/inventory.json")
	}
	if _, ok := values["safety_validate_certs"].(bool); !ok {
		return errors.New("safety_validate_certs must be an explicit boolean")
	}
	keyInfo, err := os.Lstat(keyPath)
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode()&os.ModeSymlink != 0 || keyInfo.Mode().Perm()&0077 != 0 {
		return errors.New("SSH private key must be an existing private regular file")
	}
	items, ok := values["cluster_fleet"].([]any)
	if !ok || len(items) != 6 {
		return errors.New("discovery fleet must contain exactly six VMs")
	}
	seen := map[int]bool{}
	macs := map[string]bool{}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return errors.New("invalid cluster fleet entry")
		}
		id, ok := planInt(m["vm_id"])
		if !ok {
			return errors.New("invalid cluster fleet VM ID")
		}
		var spec *VM
		for i := range t.VMs {
			if t.VMs[i].ID == id {
				spec = &t.VMs[i]
				break
			}
		}
		if spec == nil || seen[id] || m["name"] != spec.Name || m["role"] != spec.Role {
			return errors.New("discovery fleet does not match configured target")
		}
		allowed := map[string]bool{"vm_id": true, "name": true, "role": true, "tags": true, "mac": true, "management_mac": true, "ansible_user": true}
		for key := range m {
			if !allowed[key] {
				return errors.New("cluster fleet entry contains unsupported keys")
			}
		}
		if spec.Node != node {
			return errors.New("configured cluster VMs do not match discovery Proxmox node")
		}
		seen[id] = true
		for _, key := range []string{"mac", "management_mac"} {
			s, ok := m[key].(string)
			if !ok || !validMAC(s) {
				return errors.New("cluster fleet MACs are required")
			}
			s = strings.ToLower(s)
			if macs[s] {
				return errors.New("cluster fleet MACs must be unique")
			}
			macs[s] = true
		}
		tags, ok := m["tags"].([]any)
		if !ok {
			return errors.New("cluster fleet tags are required")
		}
		for _, tag := range spec.Tags {
			if !containsAny(tags, tag) {
				return errors.New("cluster fleet ownership tags mismatch")
			}
		}
		if len(tags) != len(spec.Tags) {
			return errors.New("cluster fleet tags do not exactly match configured target")
		}
		if user, exists := m["ansible_user"]; exists {
			u, ok := user.(string)
			if !ok || u == "" || strings.ContainsAny(u, " \t\r\n") {
				return errors.New("cluster fleet Ansible user is invalid")
			}
		}
	}
	return nil
}
func containsAny(a []any, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}

func validateDiscoveredInventory(path string, t Target) error {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return errors.New("discovery inventory must be a private regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return errors.New("cannot read discovery inventory")
	}
	var doc map[string]any
	if json.Unmarshal(b, &doc) != nil {
		return errors.New("discovery inventory is invalid")
	}
	varsPath := t.DiscoveryVars
	if !filepath.IsAbs(varsPath) {
		varsPath = filepath.Join(filepath.Dir(filepath.Dir(path)), filepath.Base(varsPath))
	}
	varsBytes, err := os.ReadFile(varsPath)
	if err != nil {
		return errors.New("cannot read discovery vars for inventory validation")
	}
	var discoveryVars map[string]any
	if json.Unmarshal(varsBytes, &discoveryVars) != nil {
		return errors.New("discovery vars are invalid")
	}
	privateDir, _ := discoveryVars["cluster_private_dir"].(string)
	keyPath, _ := discoveryVars["cluster_ssh_private_key_file"].(string)
	if keyPath == "" || privateDir == "" {
		return errors.New("discovery SSH inputs are incomplete")
	}
	all, _ := doc["all"].(map[string]any)
	children, _ := all["children"].(map[string]any)
	if len(doc) != 1 || len(all) != 2 || len(children) != 1 {
		return errors.New("discovered inventory has unexpected groups or fields")
	}
	allVars, _ := all["vars"].(map[string]any)
	if allVars == nil || len(allVars) != 2 {
		return errors.New("discovered inventory global variables do not match contract")
	}
	if _, ok := allVars["safety_validate_certs"].(bool); !ok && !isBoolString(allVars["safety_validate_certs"]) {
		return errors.New("discovered inventory certificate-validation setting is invalid")
	}
	nodes, _ := children["labfleet_nodes"].(map[string]any)
	groups, _ := nodes["children"].(map[string]any)
	if nodes == nil || len(nodes) != 1 || len(groups) != 2 || groups["labfleet_control_plane"] == nil || groups["labfleet_workers"] == nil {
		return errors.New("discovered inventory group structure does not match contract")
	}
	seen := map[string]bool{}
	roleCounts := map[string]int{"control-plane": 0, "worker": 0}
	controlPlaneEndpoint := ""
	for _, group := range []string{"labfleet_control_plane", "labfleet_workers"} {
		g, _ := groups[group].(map[string]any)
		if g == nil || len(g) != 1 {
			return errors.New("discovered inventory role group structure is invalid")
		}
		hosts, _ := g["hosts"].(map[string]any)
		role := "control-plane"
		if group == "labfleet_workers" {
			role = "worker"
		}
		for name, hostValue := range hosts {
			if seen[name] {
				return errors.New("duplicate discovered inventory host")
			}
			seen[name] = true
			host, ok := hostValue.(map[string]any)
			if !ok {
				return errors.New("discovered inventory host variables are invalid")
			}
			allowedHostKeys := map[string]bool{"ansible_host": true, "ansible_user": true, "ansible_ssh_private_key_file": true, "ansible_ssh_common_args": true, "labfleet_issue": true, "labfleet_kubernetes_role": true, "labfleet_expected_hostname": true, "labfleet_disposable": true, "labfleet_vm_id": true, "labfleet_proxmox_node": true, "kubernetes_node_ip": true}
			if len(host) != len(allowedHostKeys) {
				return errors.New("discovered inventory host variables do not match contract")
			}
			for key := range host {
				if !allowedHostKeys[key] {
					return errors.New("discovered inventory contains unsupported host variables")
				}
			}
			spec := findVMByName(t, name)
			if spec == nil || spec.Role != role {
				return errors.New("discovered inventory host is in the wrong role group")
			}
			id, ok := planInt(host["labfleet_vm_id"])
			if !ok || id != spec.ID || host["labfleet_expected_hostname"] != spec.Name || host["labfleet_kubernetes_role"] != role || host["labfleet_proxmox_node"] != spec.Node || host["labfleet_issue"] != "issue8" || host["labfleet_disposable"] != true {
				return errors.New("discovered inventory identity does not match target contract")
			}
			if !validIPv4(host["ansible_host"]) || !validIPv4(host["kubernetes_node_ip"]) {
				return errors.New("discovered inventory has invalid management address")
			}
			if role == "control-plane" && name == "labfleet-cp-01" {
				controlPlaneEndpoint = host["kubernetes_node_ip"].(string) + ":6443"
			}
			if host["ansible_user"] != "fleet" || host["ansible_ssh_private_key_file"] != keyPath || host["ansible_ssh_common_args"] != "-o UserKnownHostsFile="+filepath.Join(privateDir, "known_hosts")+" -o HostKeyAlias="+name+" -o StrictHostKeyChecking=yes" {
				return errors.New("discovered inventory SSH configuration does not match pinned contract")
			}
			roleCounts[role]++
		}
	}
	if len(seen) != len(t.VMs) {
		return errors.New("discovered inventory does not match configured cluster size")
	}
	for _, v := range t.VMs {
		if !seen[v.Name] {
			return errors.New("discovered inventory host is outside configured target")
		}
	}
	if roleCounts["control-plane"] != 3 || roleCounts["worker"] != 3 {
		return errors.New("discovered inventory role counts are invalid")
	}
	if allVars["kubernetes_control_plane_endpoint"] != controlPlaneEndpoint {
		return errors.New("discovered Kubernetes endpoint does not match first control plane")
	}
	return nil
}
func isBoolString(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, err := strconv.ParseBool(strings.ToLower(s))
	return err == nil
}
func validMAC(s string) bool {
	p := strings.Split(s, ":")
	if len(p) != 6 {
		return false
	}
	for _, x := range p {
		if len(x) != 2 {
			return false
		}
		if _, err := strconv.ParseUint(x, 16, 8); err != nil {
			return false
		}
	}
	return true
}
func validIPv4(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}
func findVMByName(t Target, name string) *VM {
	for i := range t.VMs {
		if t.VMs[i].Name == name {
			return &t.VMs[i]
		}
	}
	return nil
}
func insecureProvider() bool { return os.Getenv("PROXMOX_VE_INSECURE") == "true" }
func confirmTarget(in io.Reader, target string) bool {
	if in == nil || target == "" {
		return false
	}
	b := bufio.NewReader(io.LimitReader(in, 256))
	line, e := b.ReadString('\n')
	if e != nil && e != io.EOF {
		return false
	}
	return strings.TrimSpace(line) == target
}
func confirmDestroy(in io.Reader, progress io.Writer, target string) bool {
	if progress != nil {
		fmt.Fprintf(progress, "Type target name %q to confirm destruction: ", target)
	}
	return confirmTarget(in, target)
}
func confirmDestroyContext(ctx context.Context, in io.Reader, progress io.Writer, target string) bool {
	if ctx.Err() != nil {
		return false
	}
	done := make(chan bool, 1)
	go func() { done <- confirmDestroy(in, progress, target) }()
	select {
	case confirmed := <-done:
		return confirmed
	case <-ctx.Done():
		return false
	}
}
func runnerFailure(command string, err error) envelope {
	var ee *runner.ExecutionError
	if errors.As(err, &ee) {
		if ee.Canceled {
			return lifecycleError(command, "canceled", err.Error(), 130, nil)
		}
		var code *int
		if ee.ExitCode >= 0 {
			v := ee.ExitCode
			code = &v
		}
		return lifecycleError(command, "tool_failure", err.Error(), 1, code)
	}
	return lifecycleError(command, "tool_failure", "lifecycle tool execution failed", 1, nil)
}
func lifecycleError(command, class, message string, code int, toolCode *int) envelope {
	return envelope{Command: command, Status: "error", ExitCode: code, Error: &problem{Classification: class, Message: message, ToolExitCode: toolCode}}
}
