package fleetctl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

type planAction struct {
	VMID   int    `json:"vm_id"`
	Name   string `json:"name"`
	Action string `json:"action"`
}

var tofuVMAddress = regexp.MustCompile(`^proxmox_virtual_environment_vm\.labfleet(?:\[(?:[0-9]+|"[^"]+")\])?$`)

type stateModule struct {
	Resources []struct {
		Address string         `json:"address"`
		Type    string         `json:"type"`
		Mode    string         `json:"mode"`
		Values  map[string]any `json:"values"`
	} `json:"resources"`
	ChildModules []*stateModule `json:"child_modules"`
}

func validatePriorModule(module *stateModule, want map[int]VM) error {
	if module == nil {
		return nil
	}
	for _, resource := range module.Resources {
		if resource.Mode == "data" {
			continue
		}
		if resource.Mode != "managed" || resource.Type != "proxmox_virtual_environment_vm" || !tofuVMAddress.MatchString(resource.Address) {
			return errors.New("prior state contains unsupported managed resource")
		}
		id, ok := planInt(resource.Values["vm_id"])
		name, _ := resource.Values["name"].(string)
		node, _ := resource.Values["node_name"].(string)
		v, exists := want[id]
		if !ok || !exists || v.Name != name || v.Node != node {
			return errors.New("prior managed VM is outside configured target scope")
		}
		tags := planTags(resource.Values["tags"])
		for _, tag := range append(append([]string{}, v.Tags...), "labfleet", "disposable") {
			if !has(tags, tag) {
				return errors.New("prior managed VM ownership does not match target")
			}
		}
	}
	for _, child := range module.ChildModules {
		if err := validatePriorModule(child, want); err != nil {
			return err
		}
	}
	return nil
}

func lifecycleRoot(c Config, t Target) (string, error) {
	if c.RepoRoot == "" || c.WorkDir == "" || t.TofuRoot == "" {
		return "", errors.New("lifecycle paths are required")
	}
	root := t.TofuRoot
	if !filepath.IsAbs(root) {
		root = filepath.Join(c.RepoRoot, root)
	}
	root = filepath.Clean(root)
	if !within(c.RepoRoot, root) {
		return "", errors.New("tofu root is outside repository")
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return "", errors.New("tofu root unavailable")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !within(c.RepoRoot, resolved) {
		return "", errors.New("tofu root resolves outside repository")
	}
	return resolved, nil
}

// validateTargetPlan fails closed unless every managed change is scoped to a
// configured disposable VM and the phase has exactly its permitted actions.
func validateTargetPlan(data []byte, t Target, phase string, destroy bool) ([]planAction, error) {
	var p struct {
		PriorState struct {
			Values struct {
				RootModule *stateModule `json:"root_module"`
			} `json:"values"`
		} `json:"prior_state"`
		ResourceChanges []struct {
			Address, Type, Mode string
			Change              struct {
				Actions       []string
				Before, After map[string]any
				Importing     any
				Unknown       map[string]any `json:"after_unknown"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&p); err != nil {
		return nil, errors.New("invalid plan JSON")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("invalid trailing plan JSON")
	}
	want := map[int]VM{}
	for _, v := range t.VMs {
		if v.ID <= 0 || v.Name == "" || v.Node == "" || want[v.ID].ID != 0 {
			return nil, errors.New("invalid target VM scope")
		}
		want[v.ID] = v
	}
	if err := validatePriorModule(p.PriorState.Values.RootModule, want); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	out := []planAction{}
	for _, rc := range p.ResourceChanges {
		if rc.Mode == "data" {
			continue
		}
		if rc.Mode != "managed" || rc.Type != "proxmox_virtual_environment_vm" || !tofuVMAddress.MatchString(rc.Address) || rc.Change.Importing != nil {
			return nil, errors.New("plan contains unsupported managed resource")
		}
		for _, k := range []string{"vm_id", "name", "node_name", "tags"} {
			if unknownPlan(rc.Change.Unknown[k]) {
				return nil, errors.New("plan has unknown VM safety identity")
			}
		}
		action := strings.Join(rc.Change.Actions, ",")
		values := rc.Change.After
		if destroy {
			values = rc.Change.Before
			if action != "delete" {
				return nil, errors.New("destroy plan contains non-delete action")
			}
		} else {
			if action != "create" && action != "update" && action != "no-op" {
				return nil, errors.New("plan contains destructive or unsupported action")
			}
		}
		id, ok := planInt(values["vm_id"])
		name, _ := values["name"].(string)
		node, _ := values["node_name"].(string)
		if !ok || name == "" || node == "" {
			return nil, errors.New("plan lacks explicit VM identity")
		}
		v, ok := want[id]
		if !ok || v.Name != name || v.Node != node {
			return nil, errors.New("plan VM is outside configured target scope")
		}
		tags := planTags(values["tags"])
		for _, tag := range append(append([]string{}, v.Tags...), "labfleet", "disposable") {
			if !has(tags, tag) {
				return nil, errors.New("plan VM ownership tags do not match configured target")
			}
		}
		if action == "update" && !destroy {
			before := rc.Change.Before
			if before["vm_id"] != values["vm_id"] || before["name"] != values["name"] || before["node_name"] != values["node_name"] || !samePlanTags(planTags(before["tags"]), tags) {
				return nil, errors.New("update changes VM identity or ownership tags")
			}
		}
		if phase == "infrastructure" && !destroy && action == "create" {
			started, ok := values["started"].(bool)
			if !ok || started || unknownPlan(rc.Change.Unknown["started"]) {
				return nil, errors.New("infrastructure create must be explicitly stopped")
			}
		}
		if phase == "bootstrap" && !destroy && action != "no-op" {
			return nil, errors.New("bootstrap phase requires a no-op infrastructure plan")
		}
		if phase == "start" && !destroy {
			if action == "update" {
				before := rc.Change.Before
				after := rc.Change.After
				started, ok := before["started"].(bool)
				next, nextOK := after["started"].(bool)
				if !ok || !nextOK || started || !next {
					return nil, errors.New("start update must only transition started from false to true")
				}
				beforeCopy := map[string]any{}
				afterCopy := map[string]any{}
				for k, v := range before {
					if k != "started" {
						beforeCopy[k] = v
					}
				}
				for k, v := range after {
					if k != "started" {
						afterCopy[k] = v
					}
				}
				if !reflect.DeepEqual(beforeCopy, afterCopy) {
					return nil, errors.New("start update changes fields beyond started")
				}
			}
			if action != "update" && action != "no-op" {
				return nil, errors.New("start phase permits only started transition or no-op")
			}
		}
		if seen[id] {
			return nil, errors.New("duplicate VM change")
		}
		seen[id] = true
		out = append(out, planAction{id, name, action})
	}
	if len(seen) != len(want) {
		return nil, fmt.Errorf("plan does not account for every configured VM")
	}
	return out, nil
}
func unknownPlan(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case []any:
		for _, i := range x {
			if unknownPlan(i) {
				return true
			}
		}
	case map[string]any:
		for _, i := range x {
			if unknownPlan(i) {
				return true
			}
		}
	case nil:
		return false
	default:
		return true
	}
	return false
}
func planInt(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		x, e := strconv.Atoi(string(n))
		return x, e == nil
	case float64:
		return int(n), n == float64(int(n))
	case int:
		return n, true
	}
	return 0, false
}
func planTags(v any) []string {
	switch x := v.(type) {
	case string:
		return strings.Split(x, ";")
	case []any:
		o := []string{}
		for _, z := range x {
			if s, ok := z.(string); ok {
				o = append(o, s)
			}
		}
		return o
	}
	return nil
}
func samePlanTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !has(b, x) {
			return false
		}
	}
	return true
}
