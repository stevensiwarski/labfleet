package fleetctl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configFixture(t *testing.T) (string, map[string]any) {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"infra/opentofu", "infra/opentofu/cluster"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0700); err != nil {
			t.Fatal(err)
		}
	}
	vars := filepath.Join(root, "secrets.tfvars")
	if err := os.WriteFile(vars, []byte("secret = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	c := map[string]any{"repo_root": root, "work_dir": work, "targets": map[string]any{"safe": map[string]any{"kind": "vm", "tofu_root": "infra/opentofu", "vars_file": "secrets.tfvars", "vms": []any{map[string]any{"id": 900013, "name": "labfleet-test-01", "node": "node1", "role": "disposable", "tags": []string{"labfleet", "disposable", "issue13"}}}}}}
	return root, c
}

func writeConfig(t *testing.T, c map[string]any) string {
	t.Helper()
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestReadConfigDefaultsAndValidation(t *testing.T) {
	_, c := configFixture(t)
	got, e := ReadConfig(writeConfig(t, c))
	if e != nil {
		t.Fatal(e)
	}
	if got.TimeoutSeconds != 1800 || got.Tofu != "tofu" || got.Ansible != "ansible-playbook" || got.Kubectl != "kubectl" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestReadConfigRejectsUnsafeConfigurations(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unknown field":          func(c map[string]any) { c["surprise"] = true },
		"invalid timeout":        func(c map[string]any) { c["timeout_seconds"] = 3601 },
		"unknown default target": func(c map[string]any) { c["default_target"] = "missing" },
		"empty VMs":              func(c map[string]any) { c["targets"].(map[string]any)["safe"].(map[string]any)["vms"] = []any{} },
		"protected ID": func(c map[string]any) {
			c["targets"].(map[string]any)["safe"].(map[string]any)["vms"].([]any)[0].(map[string]any)["id"] = 930040
		},
		"unsafe tags": func(c map[string]any) {
			c["targets"].(map[string]any)["safe"].(map[string]any)["vms"].([]any)[0].(map[string]any)["tags"] = []string{"labfleet", "disposable", "bootstrap"}
		},
		"invalid target name": func(c map[string]any) {
			ts := c["targets"].(map[string]any)
			ts["-bad"] = ts["safe"]
			delete(ts, "safe")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, c := configFixture(t)
			if _, err := ReadConfig(writeConfig(t, c)); err != nil {
				t.Fatalf("baseline configuration must be valid: %v", err)
			}
			mutate(c)
			_, e := ReadConfig(writeConfig(t, c))
			if e == nil {
				t.Fatal("expected rejection")
			}
			if name == "protected ID" && e.Error() != "protected VM" {
				t.Fatalf("expected explicit protected-ID guard, got: %v", e)
			}
		})
	}
}

func TestReadConfigRejectsSymlinkWorkdirAndVarsfile(t *testing.T) {
	root, c := configFixture(t)
	real := filepath.Join(root, "real")
	if e := os.Mkdir(real, 0700); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "linked")
	if e := os.Symlink(real, link); e != nil {
		t.Fatal(e)
	}
	c["work_dir"] = link
	if _, e := ReadConfig(writeConfig(t, c)); e == nil {
		t.Fatal("accepted symlink workdir")
	}
	root, c = configFixture(t)
	target := c["targets"].(map[string]any)["safe"].(map[string]any)
	secret := filepath.Join(root, "secrets.tfvars")
	os.Remove(secret)
	os.Symlink(filepath.Join(root, "outside"), secret)
	target["vars_file"] = "secrets.tfvars"
	if _, e := ReadConfig(writeConfig(t, c)); e == nil {
		t.Fatal("accepted symlink vars file")
	}
}

func TestReadConfigRejectsTrailingJSON(t *testing.T) {
	_, c := configFixture(t)
	p := writeConfig(t, c)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(" {} ")
	f.Close()
	if _, e := ReadConfig(p); e == nil || !strings.Contains(e.Error(), "invalid") {
		t.Fatal("trailing JSON accepted")
	}
}

func TestReadConfigRequiresPrivateNonsymlinkFile(t *testing.T) {
	_, c := configFixture(t)
	p := writeConfig(t, c)
	if err := os.Chmod(p, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(p); err == nil {
		t.Fatal("accepted world-readable config")
	}
	link := p + ".link"
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(link); err == nil {
		t.Fatal("accepted config symlink")
	}
}
