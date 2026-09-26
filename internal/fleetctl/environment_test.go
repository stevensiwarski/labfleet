package fleetctl

import (
	"encoding/json"
	"testing"
)

func TestValidateExecutionEnvironment(t *testing.T) {
	for _, key := range []string{"TF_CLI_ARGS", "TF_CLI_ARGS_plan", "TF_DATA_DIR", "TF_CLI_CONFIG_FILE", "TF_VAR_vm_count", "ANSIBLE_CONFIG", "ANSIBLE_INVENTORY", "ANSIBLE_ROLES_PATH"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "ambient-value")
			if err := validateExecutionEnvironment(); err == nil {
				t.Fatalf("expected %s to be rejected", key)
			}
		})
	}
	t.Run("non-default workspace", func(t *testing.T) {
		t.Setenv("TF_WORKSPACE", "other")
		if err := validateExecutionEnvironment(); err == nil {
			t.Fatal("expected non-default workspace to be rejected")
		}
	})
	t.Run("default workspace", func(t *testing.T) {
		t.Setenv("TF_WORKSPACE", "default")
		if err := validateExecutionEnvironment(); err != nil {
			t.Fatalf("default workspace should be allowed: %v", err)
		}
	})
}

func TestValidatePlanProvider(t *testing.T) {
	t.Setenv("PROXMOX_VE_ENDPOINT", "https://provider.example/")
	t.Setenv("PROXMOX_VE_INSECURE", "false")
	plan := func(endpoint any, insecure any) []byte {
		t.Helper()
		data, err := json.Marshal(map[string]any{"variables": map[string]any{
			"proxmox_endpoint": map[string]any{"value": endpoint},
			"tls_insecure":     map[string]any{"value": insecure},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tc := range []struct {
		name     string
		endpoint any
		insecure any
		wantErr  bool
	}{
		{"matching", "https://provider.example", false, false},
		{"endpoint mismatch", "https://other.example", false, true},
		{"TLS mismatch", "https://provider.example", true, true},
		{"missing endpoint uses env", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePlanProvider(plan(tc.endpoint, tc.insecure), "vm")
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePlanProvider() error = %v, want error %v", err, tc.wantErr)
			}
		})
	}
	if err := validatePlanProvider([]byte(`{"variables":{}}`), "vm"); err == nil {
		t.Fatal("accepted plan with no provider variables")
	}
	if err := validatePlanProvider([]byte(`{"variables":{"tls_insecure":{"value":false}}}`), "cluster"); err != nil {
		t.Fatalf("cluster root uses endpoint from environment: %v", err)
	}
}
