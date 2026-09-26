package fleetctl

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const protectedVMID = 930040

type Node struct {
	VMID  int      `json:"vm_id"`
	Name  string   `json:"name"`
	Node  string   `json:"node"`
	Role  string   `json:"role"`
	Tags  []string `json:"tags"`
	State string   `json:"state"`
	Owned bool     `json:"owned"`
	Ready *bool    `json:"ready,omitempty"`
}
type provider struct {
	base   string
	token  string
	client *http.Client
}

func newProviderFromEnv() (*provider, error) {
	raw := os.Getenv("PROXMOX_VE_ENDPOINT")
	tok := os.Getenv("PROXMOX_VE_API_TOKEN")
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid Proxmox API URL configuration")
	}
	if tok == "" {
		return nil, errors.New("Proxmox API token is not configured")
	}
	if !strings.HasPrefix(tok, "PVEAPIToken=") {
		tok = "PVEAPIToken=" + tok
	}
	if strings.ContainsAny(tok, "\r\n") {
		return nil, errors.New("invalid Proxmox API token configuration")
	}
	insecure := false
	if v := os.Getenv("PROXMOX_VE_INSECURE"); v != "" {
		if v != "true" && v != "false" {
			return nil, errors.New("PROXMOX_VE_INSECURE must be true or false")
		}
		insecure = v == "true"
	}
	tl := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} // #nosec G402 -- only explicit operator opt-in
	tr := &http.Transport{TLSClientConfig: tl, Proxy: nil}
	return &provider{base: strings.TrimRight(raw, "/"), token: tok, client: &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type pveResource struct {
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Tags   string `json:"tags"`
}

func (p *provider) get(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if e != nil {
		return errors.New("invalid provider request")
	}
	req.Header.Set("Authorization", p.token)
	r, e := p.client.Do(req)
	if e != nil {
		return errors.New("Proxmox API request failed")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("Proxmox API returned HTTP %d", r.StatusCode)
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	var wrap struct {
		Data json.RawMessage `json:"data"`
	}
	if e = dec.Decode(&wrap); e != nil {
		return errors.New("invalid Proxmox API response")
	}
	if e = json.Unmarshal(wrap.Data, out); e != nil {
		return errors.New("invalid Proxmox API response")
	}
	return nil
}
func (p *provider) inventory(ctx context.Context) ([]Node, error) {
	var rows []pveResource
	if e := p.get(ctx, "/api2/json/cluster/resources?type=vm", &rows); e != nil {
		return nil, e
	}
	out := []Node{}
	seen := map[int]bool{}
	for _, x := range rows {
		if seen[x.VMID] {
			return nil, errors.New("duplicate VM identity in provider inventory")
		}
		seen[x.VMID] = true
		if x.Type != "qemu" || x.VMID == protectedVMID || protectedName(x.Name) || !strings.HasPrefix(x.Name, "labfleet-") {
			continue
		}
		tags := []string{}
		for _, t := range strings.Split(x.Tags, ";") {
			if t != "" {
				tags = append(tags, t)
			}
		}
		owned := has(tags, "labfleet") && has(tags, "disposable") && x.VMID >= 900000 && x.VMID < 1000000 && !has(tags, "bootstrap") && !has(tags, "control") && !has(tags, "provisioner")
		if !owned {
			continue
		}
		role := "disposable"
		for _, candidate := range []string{"control-plane", "worker", "test"} {
			if has(tags, candidate) {
				role = candidate
				break
			}
		}
		out = append(out, Node{VMID: x.VMID, Name: x.Name, Node: x.Node, Role: role, Tags: tags, State: x.Status, Owned: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VMID < out[j].VMID })
	return out, nil
}

func protectedName(name string) bool {
	return strings.Contains(name, "provisioner") || strings.Contains(name, "coding-agent")
}

// provisioner returns the protected service VM's observed provider state without
// treating it as a disposable managed node or making any service-health claim.
func (p *provider) provisioner(ctx context.Context) (*Node, error) {
	var rows []pveResource
	if err := p.get(ctx, "/api2/json/cluster/resources?type=vm", &rows); err != nil {
		return nil, err
	}
	for _, x := range rows {
		if x.VMID == protectedVMID || x.Name == "labfleet-provisioner" {
			return &Node{VMID: x.VMID, Name: x.Name, Node: x.Node, State: x.Status, Owned: false}, nil
		}
	}
	return nil, nil
}
func (p *provider) attest(ctx context.Context, v VM) error {
	var rows []pveResource
	if e := p.get(ctx, "/api2/json/cluster/resources?type=vm", &rows); e != nil {
		return e
	}
	var found *pveResource
	for i := range rows {
		if rows[i].VMID == v.ID {
			found = &rows[i]
			break
		}
	}
	if found == nil || found.Type != "qemu" || found.Name != v.Name || found.Node != v.Node || found.VMID < 900000 || found.VMID > 999999 || found.VMID == protectedVMID || protectedName(found.Name) {
		return errors.New("configured VM identity does not match live inventory")
	}
	tags := strings.Split(found.Tags, ";")
	if !has(tags, "labfleet") || !has(tags, "disposable") || has(tags, "bootstrap") || has(tags, "control") || has(tags, "provisioner") {
		return errors.New("configured VM ownership tags do not match")
	}
	var config struct {
		Tags       string          `json:"tags"`
		Protection json.RawMessage `json:"protection"`
	}
	if e := p.get(ctx, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/config", url.PathEscape(v.Node), v.ID), &config); e != nil {
		return e
	}
	actual := strings.Split(config.Tags, ";")
	for _, tag := range v.Tags {
		if !has(actual, tag) {
			return errors.New("configured VM tags do not match live configuration")
		}
	}
	if len(config.Protection) != 0 && string(config.Protection) != "0" && string(config.Protection) != "false" {
		return errors.New("configured VM is protected")
	}
	return nil
}
