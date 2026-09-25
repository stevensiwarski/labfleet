// Package tofusafety validates saved OpenTofu plans against live Proxmox ownership.
package tofusafety

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const resourceType = "proxmox_virtual_environment_vm"
const resourceAddress = resourceType + ".labfleet"
const minVMID = 900000
const maxVMID = 999999

var addressPattern = regexp.MustCompile(`^proxmox_virtual_environment_vm\.labfleet(?:\[(?:[0-9]+|"[^"]+")\])?$`)

type vm struct {
	ID         int
	Name, Node string
	Tags       []string
	QEMU       bool
}
type resource struct {
	Address      string         `json:"address"`
	Type         string         `json:"type"`
	Mode         string         `json:"mode"`
	Index        any            `json:"index"`
	Values       map[string]any `json:"values"`
	Resources    []resource     `json:"resources"`
	ChildModules []resource     `json:"child_modules"`
}
type changeData struct {
	Actions   []string       `json:"actions"`
	Before    map[string]any `json:"before"`
	After     map[string]any `json:"after"`
	Importing any            `json:"importing"`
	Unknown   map[string]any `json:"after_unknown"`
}
type planChange struct {
	Address string     `json:"address"`
	Type    string     `json:"type"`
	Mode    string     `json:"mode"`
	Change  changeData `json:"change"`
}
type plan struct {
	FormatVersion    string            `json:"format_version"`
	TerraformVersion string            `json:"terraform_version"`
	Complete         *bool             `json:"complete"`
	Errored          *bool             `json:"errored"`
	Deferred         []json.RawMessage `json:"deferred_changes"`
	ResourceChanges  []planChange      `json:"resource_changes"`
	PriorState       struct {
		Values struct {
			Root *resource `json:"root_module"`
		} `json:"values"`
	} `json:"prior_state"`
}

// CheckFile returns the count of authorized VM changes.
func CheckFile(path, endpoint, token string, insecure bool) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, errors.New("cannot read plan")
	}
	return Check(b, endpoint, token, insecure)
}

// Check consumes tofu show -json output and performs only a read-only Proxmox inventory request.
func Check(data []byte, endpoint, token string, insecure bool) (int, error) {
	var p plan
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&p); err != nil {
		return 0, errors.New("invalid plan JSON")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return 0, errors.New("invalid trailing plan JSON")
	}
	if !strings.HasPrefix(p.FormatVersion, "1.") || p.TerraformVersion == "" || p.ResourceChanges == nil || p.Errored == nil {
		return 0, errors.New("malformed or unsupported plan format")
	}
	if *p.Errored || (p.Complete != nil && !*p.Complete) {
		return 0, errors.New("plan is incomplete or errored")
	}
	if len(p.Deferred) > 0 {
		return 0, errors.New("plan contains deferred changes")
	}
	if p.PriorState.Values.Root != nil {
		if err := walkState(p.PriorState.Values.Root); err != nil {
			return 0, err
		}
	}
	ids := map[int]string{}
	addresses := map[string]bool{}
	for _, c := range p.ResourceChanges {
		if c.Mode == "data" {
			continue
		}
		if c.Mode != "managed" || c.Type == "" || !validAddress(c.Address) {
			return 0, errors.New("managed plan change has invalid mode, type, or address")
		}
		if addresses[c.Address] {
			return 0, errors.New("duplicate managed resource address")
		}
		addresses[c.Address] = true
		if c.Type != resourceType {
			return 0, errors.New("unsupported managed resource in plan")
		}
		if c.Change.Importing != nil {
			return 0, errors.New("imports are not allowed")
		}
		if len(c.Change.Actions) == 0 {
			return 0, errors.New("managed change has no actions")
		}
		for _, v := range []map[string]any{c.Change.Before, c.Change.After} {
			if v != nil {
				if id, ok := number(v["vm_id"]); ok {
					if owner, exists := ids[id]; exists && owner != c.Address {
						return 0, errors.New("duplicate VM ID in plan")
					}
					ids[id] = c.Address
				}
			}
		}
		for _, k := range []string{"vm_id", "name", "node_name", "tags"} {
			if v, exists := c.Change.Unknown[k]; exists && hasUnknown(v) {
				return 0, errors.New("plan has unknown VM safety identity")
			}
		}
	}
	live, err := fetch(endpoint, token, insecure)
	if err != nil {
		return 0, err
	}
	byID := map[int]vm{}
	for _, v := range live {
		if _, dup := byID[v.ID]; dup {
			return 0, errors.New("duplicate VM ID in Proxmox inventory")
		}
		byID[v.ID] = v
	}
	if err := verifyState(p.PriorState.Values.Root, byID); err != nil {
		return 0, err
	}
	count := 0
	for _, c := range p.ResourceChanges {
		if c.Mode == "data" {
			continue
		}
		action := strings.Join(c.Change.Actions, ",")
		if action == "create" {
			id, ok := number(c.Change.After["vm_id"])
			name, _ := c.Change.After["name"].(string)
			node, _ := c.Change.After["node_name"].(string)
			if !ok || id < minVMID || id > maxVMID || !strings.HasPrefix(name, "labfleet-") || node == "" || !contains(tagsOf(c.Change.After["tags"]), "labfleet") {
				return 0, errors.New("create lacks explicit LabFleet identity")
			}
			if _, exists := byID[id]; exists {
				return 0, errors.New("VM ID already exists in Proxmox")
			}
			count++
			continue
		}
		if action != "update" && action != "delete" && action != "no-op" {
			return 0, errors.New("replacement or unsupported VM action")
		}
		id, ok := number(c.Change.Before["vm_id"])
		if !ok || id < minVMID || id > maxVMID || !providerIDMatches(c.Change.Before, id) {
			return 0, errors.New("unsafe prior VM ID")
		}
		old, exists := byID[id]
		if !exists || !old.QEMU || !owned(old) {
			return 0, errors.New("prior VM is not a live LabFleet VM")
		}
		if c.Change.Before["name"] != old.Name || c.Change.Before["node_name"] != old.Node {
			return 0, errors.New("prior VM identity does not match live Proxmox")
		}
		if action == "update" || action == "no-op" {
			afterID, ok := number(c.Change.After["vm_id"])
			if !ok || afterID != id || !providerIDMatches(c.Change.After, id) || c.Change.After["name"] != old.Name || c.Change.After["node_name"] != old.Node || !contains(tagsOf(c.Change.After["tags"]), "labfleet") {
				return 0, errors.New("update changes or removes owned VM identity")
			}
		}
		if action != "no-op" {
			count++
		}
	}
	return count, nil
}
func hasUnknown(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case []any:
		for _, item := range x {
			if hasUnknown(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range x {
			if hasUnknown(item) {
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
func validAddress(s string) bool { return addressPattern.MatchString(s) }
func walkState(r *resource) error {
	if r == nil {
		return nil
	}
	for _, x := range r.Resources {
		if x.Mode != "managed" && x.Mode != "data" {
			return errors.New("state resource has invalid mode")
		}
		if x.Mode == "managed" && (x.Type == "" || !validAddress(x.Address) || x.Type != resourceType) {
			return errors.New("unsupported managed resource in state")
		}
		if x.Mode == "managed" {
			if _, ok := number(x.Values["vm_id"]); !ok {
				return errors.New("managed VM state has unknown ID")
			}
		}
	}
	for i := range r.Resources {
		if e := walkState(&r.Resources[i]); e != nil {
			return e
		}
	}
	for i := range r.ChildModules {
		if e := walkState(&r.ChildModules[i]); e != nil {
			return e
		}
	}
	return nil
}
func verifyState(r *resource, live map[int]vm) error {
	if r == nil {
		return nil
	}
	for _, x := range r.Resources {
		if x.Mode == "managed" && x.Type == resourceType {
			id, ok := number(x.Values["vm_id"])
			v, found := live[id]
			if !ok || id < minVMID || id > maxVMID || !providerIDMatches(x.Values, id) || !found || !v.QEMU || !owned(v) || x.Values["name"] != v.Name || x.Values["node_name"] != v.Node {
				return errors.New("state VM does not match live LabFleet identity")
			}
		}
		if e := verifyState(&x, live); e != nil {
			return e
		}
	}
	for i := range r.ChildModules {
		if e := verifyState(&r.ChildModules[i], live); e != nil {
			return e
		}
	}
	return nil
}

// The provider targets its string id, not merely the configurable vm_id.
func providerIDMatches(values map[string]any, id int) bool {
	return values["id"] == strconv.Itoa(id)
}

func number(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, e := strconv.Atoi(string(n))
		return i, e == nil
	case float64:
		return int(n), n == float64(int(n))
	case int:
		return n, true
	}
	return 0, false
}
func tagsOf(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case []any:
		for _, t := range x {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = strings.Split(x, ";")
	}
	return out
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
func owned(v vm) bool {
	return v.ID >= minVMID && v.ID <= maxVMID && strings.HasPrefix(v.Name, "labfleet-") && contains(v.Tags, "labfleet")
}

func fetch(endpoint, token string, insecure bool) ([]vm, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid Proxmox endpoint")
	}
	if token == "" || strings.HasPrefix(token, "PVEAPIToken=") {
		return nil, errors.New("missing or invalid Proxmox API token")
	}
	u.Path = "/api2/json/cluster/resources"
	q := u.Query()
	q.Set("type", "vm")
	u.RawQuery = q.Encode()
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequest("GET", u.String(), nil)
	if e != nil {
		return nil, errors.New("invalid Proxmox request")
	}
	req.Header.Set("Authorization", "PVEAPIToken="+token)
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("Proxmox API request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("Proxmox API returned non-success status")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if e != nil || len(b) >= 4<<20 {
		return nil, errors.New("invalid Proxmox API response")
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.Data == nil {
		return nil, errors.New("invalid Proxmox API response")
	}
	out := []vm{}
	seen := map[int]bool{}
	for _, x := range envelope.Data {
		kind, ok := x["type"].(string)
		if !ok {
			return nil, errors.New("malformed Proxmox inventory entry")
		}
		id, ok := number(x["vmid"])
		if !ok || id <= 0 {
			return nil, errors.New("malformed Proxmox inventory ID")
		}
		if seen[id] {
			return nil, errors.New("duplicate VM ID in Proxmox inventory")
		}
		seen[id] = true
		if kind != "qemu" && kind != "lxc" {
			continue
		}
		name, nok := x["name"].(string)
		node, nodok := x["node"].(string)
		if !nok || !nodok || name == "" || node == "" {
			return nil, errors.New("malformed Proxmox inventory identity")
		}
		v := vm{ID: id, Name: name, Node: node, QEMU: kind == "qemu"}
		if v.QEMU && x["tags"] != nil {
			tags, tok := x["tags"].(string)
			if !tok {
				return nil, errors.New("malformed Proxmox VM tags")
			}
			v.Tags = tagsOf(tags)
		}
		out = append(out, v)
	}
	return out, nil
}
