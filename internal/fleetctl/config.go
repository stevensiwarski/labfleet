package fleetctl

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	RepoRoot       string            `json:"repo_root"`
	WorkDir        string            `json:"work_dir"`
	Tofu           string            `json:"tofu,omitempty"`
	Ansible        string            `json:"ansible_playbook,omitempty"`
	Kubectl        string            `json:"kubectl,omitempty"`
	DefaultTarget  string            `json:"default_target,omitempty"`
	Kubeconfig     string            `json:"kubeconfig,omitempty"`
	Targets        map[string]Target `json:"targets"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}
type Target struct {
	Kind          string `json:"kind"`
	TofuRoot      string `json:"tofu_root"`
	VarsFile      string `json:"vars_file"`
	VMs           []VM   `json:"vms,omitempty"`
	DiscoveryVars string `json:"discovery_vars,omitempty"`
	Inventory     string `json:"inventory,omitempty"`
}
type VM struct {
	ID   int      `json:"id"`
	Name string   `json:"name"`
	Node string   `json:"node"`
	Role string   `json:"role"`
	Tags []string `json:"tags"`
}

func ReadConfig(path string) (Config, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return Config{}, errors.New("config must be a private regular nonsymlink file")
	}
	f, e := os.Open(path)
	if e != nil {
		return Config{}, errors.New("cannot open config")
	}
	defer f.Close()
	var c Config
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return Config{}, errors.New("invalid config")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return Config{}, errors.New("invalid config")
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return Config{}, errors.New("invalid config")
	}
	if !filepath.IsAbs(c.RepoRoot) || !filepath.IsAbs(c.WorkDir) || len(c.Targets) == 0 {
		return Config{}, errors.New("repo_root, work_dir and targets are required")
	}
	root, e := filepath.EvalSymlinks(c.RepoRoot)
	if e != nil {
		return Config{}, errors.New("repo_root unavailable")
	}
	c.RepoRoot = root
	if st, err := os.Lstat(c.WorkDir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0700 {
		return Config{}, errors.New("work_dir must be an existing nonsymlink directory with mode 0700")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 1800
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 {
		return Config{}, errors.New("invalid timeout_seconds")
	}
	if c.Tofu == "" {
		c.Tofu = "tofu"
	}
	if c.Ansible == "" {
		c.Ansible = "ansible-playbook"
	}
	if c.Kubectl == "" {
		c.Kubectl = "kubectl"
	}
	if c.Kubeconfig == "" {
		c.Kubeconfig = os.Getenv("KUBECONFIG")
	}
	if c.Kubeconfig != "" && (!filepath.IsAbs(c.Kubeconfig) || strings.Contains(c.Kubeconfig, string(os.PathListSeparator))) {
		return Config{}, errors.New("kubeconfig must be a single absolute path")
	}
	if c.DefaultTarget != "" {
		if _, ok := c.Targets[c.DefaultTarget]; !ok {
			return Config{}, errors.New("default_target is not configured")
		}
	}
	targetPattern := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	seenIDs, seenNames := map[int]bool{}, map[string]bool{}
	for name, t := range c.Targets {
		if !targetPattern.MatchString(name) || (t.Kind != "vm" && t.Kind != "cluster") {
			return Config{}, errors.New("invalid target")
		}
		allowed := "infra/opentofu"
		if t.Kind == "cluster" {
			allowed = "infra/opentofu/cluster"
		}
		if filepath.Clean(t.TofuRoot) != allowed {
			return Config{}, errors.New("target tofu_root is not allowed")
		}
		if t.VarsFile == "" {
			return Config{}, errors.New("vars_file is required")
		}
		vars := t.VarsFile
		if !filepath.IsAbs(vars) {
			vars = filepath.Join(root, vars)
		}
		st, err := os.Lstat(vars)
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
			return Config{}, errors.New("vars_file must be an existing private regular file")
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, allowed))
		if err != nil || !within(root, resolved) {
			return Config{}, errors.New("target root unavailable")
		}
		if st, err := os.Stat(resolved); err != nil || !st.IsDir() {
			return Config{}, errors.New("target root unavailable")
		}
		if t.Kind == "cluster" && len(t.VMs) != 6 {
			return Config{}, errors.New("cluster target must specify six VMs")
		}
		if t.Kind == "vm" && len(t.VMs) == 0 {
			return Config{}, errors.New("vm target must specify VMs")
		}
		for i, v := range t.VMs {
			if v.ID < 900000 || v.ID > 999999 || !regexp.MustCompile(`^labfleet-[A-Za-z0-9_-]+$`).MatchString(v.Name) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`).MatchString(v.Node) || seenIDs[v.ID] || seenNames[v.Name] {
				return Config{}, errors.New("unsafe VM specification")
			}
			seenIDs[v.ID], seenNames[v.Name] = true, true
			if v.ID == 930040 || strings.Contains(v.Name, "provisioner") || strings.Contains(v.Name, "coding-agent") || has(v.Tags, "provisioner") || has(v.Tags, "bootstrap") || has(v.Tags, "control") {
				return Config{}, errors.New("protected VM")
			}
			if !has(v.Tags, "labfleet") || !has(v.Tags, "disposable") || v.Role == "" || !has(v.Tags, v.Role) {
				return Config{}, errors.New("unsafe VM ownership tags")
			}
			if t.Kind == "cluster" {
				ids := []int{930081, 930082, 930083, 930084, 930085, 930086}
				names := []string{"labfleet-cp-01", "labfleet-cp-02", "labfleet-cp-03", "labfleet-worker-01", "labfleet-worker-02", "labfleet-worker-03"}
				roles := []string{"control-plane", "control-plane", "control-plane", "worker", "worker", "worker"}
				if v.ID != ids[i] || v.Name != names[i] || v.Role != roles[i] || !has(v.Tags, "issue8") {
					return Config{}, errors.New("invalid cluster VM specification")
				}
			}
			if t.Kind == "vm" && v.ID >= 930081 && v.ID <= 930086 {
				return Config{}, errors.New("cluster VM in vm target")
			}
		}
		for _, p := range []string{t.DiscoveryVars, t.Inventory} {
			if p != "" && (strings.TrimSpace(p) == "" || strings.ContainsAny(p, "\x00\r\n")) {
				return Config{}, errors.New("invalid optional target path")
			}
		}
		t.TofuRoot = allowed
		t.VarsFile = filepath.Clean(vars)
		if t.DiscoveryVars != "" && !filepath.IsAbs(t.DiscoveryVars) {
			t.DiscoveryVars = filepath.Join(root, t.DiscoveryVars)
		}
		if t.Inventory != "" && !filepath.IsAbs(t.Inventory) {
			t.Inventory = filepath.Join(root, t.Inventory)
		}
		c.Targets[name] = t
	}
	return c, nil
}
func within(root, path string) bool {
	r, e := filepath.Rel(root, path)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
func has(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
