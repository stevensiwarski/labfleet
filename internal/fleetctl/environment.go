package fleetctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// validateExecutionEnvironment prevents ambient CLI configuration from changing
// the meaning or destination of a lifecycle plan.
func validateExecutionEnvironment() error {
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		unsafe := strings.HasPrefix(key, "ANSIBLE_") || strings.HasPrefix(key, "TF_VAR_") ||
			strings.HasPrefix(key, "TF_CLI_ARGS_") || key == "TF_CLI_ARGS" ||
			key == "TF_DATA_DIR" || key == "TF_CLI_CONFIG_FILE" ||
			(key == "TF_WORKSPACE" && os.Getenv(key) != "default")
		if unsafe {
			return fmt.Errorf("unsafe lifecycle environment setting: %s", key)
		}
	}
	return nil
}

// validatePlanProvider verifies the provider endpoint and TLS mode actually
// embedded in a saved plan against the explicitly configured runtime provider.
func validatePlanProvider(data []byte, targetKind string) error {
	var plan struct {
		Variables map[string]struct {
			Value any `json:"value"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return errors.New("invalid provider settings in plan")
	}
	// The reviewed cluster root binds endpoint=null directly, so the provider
	// uses PROXMOX_VE_ENDPOINT; unlike the VM root it declares no endpoint variable.
	if _, ok := plan.Variables["proxmox_endpoint"]; !ok && targetKind != "cluster" {
		return errors.New("plan is missing the provider endpoint variable")
	}
	if _, ok := plan.Variables["tls_insecure"]; !ok {
		return errors.New("plan is missing the provider TLS variable")
	}
	if raw, ok := plan.Variables["proxmox_endpoint"]; ok {
		endpoint, ok := raw.Value.(string)
		if raw.Value == nil {
			endpoint = os.Getenv("PROXMOX_VE_ENDPOINT")
		} else if !ok {
			return errors.New("invalid plan Proxmox endpoint")
		}
		if canonicalEndpoint(endpoint) == "" || canonicalEndpoint(endpoint) != canonicalEndpoint(os.Getenv("PROXMOX_VE_ENDPOINT")) {
			return errors.New("plan Proxmox endpoint does not match configured provider")
		}
	}
	if raw, ok := plan.Variables["tls_insecure"]; ok {
		insecure, ok := raw.Value.(bool)
		if !ok || insecure != insecureProvider() {
			return errors.New("plan TLS mode does not match configured provider")
		}
	}
	return nil
}

func canonicalEndpoint(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ""
	}
	return strings.TrimRight(value, "/")
}
