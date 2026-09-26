package fleetctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestValidateTargetPlanScopeAndActions(t *testing.T) {
	target := Target{VMs: []VM{{ID: 930013, Name: "labfleet-issue13-test-01", Node: "pve1", Role: "test", Tags: []string{"labfleet", "disposable", "issue13"}}}}
	makePlan := func(action string, id int) []byte {
		p := map[string]any{"resource_changes": []any{map[string]any{"address": "proxmox_virtual_environment_vm.labfleet[\"test\"]", "type": "proxmox_virtual_environment_vm", "mode": "managed", "change": map[string]any{"actions": []string{action}, "before": nil, "after": map[string]any{"vm_id": id, "name": "labfleet-issue13-test-01", "node_name": "pve1", "tags": "labfleet;disposable;issue13", "started": false}}}}}
		b, _ := json.Marshal(p)
		return b
	}
	got, err := validateTargetPlan(makePlan("create", 930013), target, "infrastructure", false)
	if err != nil || len(got) != 1 || got[0].Action != "create" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err = validateTargetPlan(makePlan("delete", 930013), target, "infrastructure", false); err == nil {
		t.Fatal("accepted destructive change")
	}
	if _, err = validateTargetPlan(makePlan("create", 100930040), target, "infrastructure", false); err == nil {
		t.Fatal("accepted out-of-scope protected ID")
	}
}

func TestTargetPlanRejectsUnsafePhaseAndMalformedTail(t *testing.T) {
	target := Target{VMs: []VM{{ID: 930013, Name: "labfleet-issue13-test-01", Node: "pve1", Tags: []string{"labfleet", "disposable"}}}}
	create := []byte(`{"resource_changes":[{"address":"proxmox_virtual_environment_vm.labfleet[\"x\"]","type":"proxmox_virtual_environment_vm","mode":"managed","change":{"actions":["create"],"after":{"vm_id":930013,"name":"labfleet-issue13-test-01","node_name":"pve1","tags":"labfleet;disposable","started":true}}}]}`)
	if _, err := validateTargetPlan(create, target, "infrastructure", false); err == nil {
		t.Fatal("infrastructure plan accepted running create")
	}
	createStopped := strings.Replace(string(create), `"started":true`, `"started":false`, 1)
	if _, err := validateTargetPlan([]byte(createStopped+` {`), target, "infrastructure", false); err == nil {
		t.Fatal("accepted malformed trailing JSON")
	}
	if _, err := validateTargetPlan([]byte(createStopped), target, "bootstrap", false); err == nil {
		t.Fatal("bootstrap accepted create instead of no-op")
	}
}

func TestStartPlanAllowsOnlyStartedTransition(t *testing.T) {
	target := Target{VMs: []VM{{ID: 930013, Name: "labfleet-issue13-test-01", Node: "pve1", Tags: []string{"labfleet", "disposable"}}}}
	plan := func(extraAfter string) []byte {
		return []byte(`{"resource_changes":[{"address":"proxmox_virtual_environment_vm.labfleet[\"x\"]","type":"proxmox_virtual_environment_vm","mode":"managed","change":{"actions":["update"],"before":{"vm_id":930013,"name":"labfleet-issue13-test-01","node_name":"pve1","tags":"labfleet;disposable","started":false},"after":{"vm_id":930013,"name":"labfleet-issue13-test-01","node_name":"pve1","tags":"labfleet;disposable","started":true` + extraAfter + `}}}]}`)
	}
	if _, err := validateTargetPlan(plan(""), target, "start", false); err != nil {
		t.Fatalf("valid started-only transition rejected: %v", err)
	}
	if _, err := validateTargetPlan(plan(`,"cores":8`), target, "start", false); err == nil {
		t.Fatal("accepted extra start mutation")
	}
}

func TestPriorStateRecursivelyMustBelongToTarget(t *testing.T) {
	target := Target{VMs: []VM{{ID: 930013, Name: "labfleet-issue13-test-01", Node: "pve1", Tags: []string{"labfleet", "disposable"}}}}
	p := []byte(`{"prior_state":{"values":{"root_module":{"child_modules":[{"resources":[{"address":"proxmox_virtual_environment_vm.labfleet[\"foreign\"]","type":"proxmox_virtual_environment_vm","mode":"managed","values":{"vm_id":930014,"name":"labfleet-foreign","node_name":"pve1","tags":"labfleet;disposable"}}]}]}}},"resource_changes":[{"address":"proxmox_virtual_environment_vm.labfleet[\"x\"]","type":"proxmox_virtual_environment_vm","mode":"managed","change":{"actions":["create"],"after":{"vm_id":930013,"name":"labfleet-issue13-test-01","node_name":"pve1","tags":"labfleet;disposable","started":false}}}]}`)
	if _, err := validateTargetPlan(p, target, "infrastructure", false); err == nil {
		t.Fatal("accepted foreign prior-state VM")
	}
}

func TestLifecycleFlowDecisions(t *testing.T) {
	if lifecyclePlanOnly("destroy", lifecycleOptions{}) {
		t.Fatal("default destroy must execute after confirmation")
	}
	if !lifecyclePlanOnly("destroy", lifecycleOptions{Plan: true}) {
		t.Fatal("explicit destroy plan must remain dry-run")
	}
	if lifecyclePlanOnly("destroy", lifecycleOptions{Apply: true, Yes: true}) {
		t.Fatal("destroy --yes must execute")
	}
	if destroyNeedsConfirmation("destroy", true) || !destroyNeedsConfirmation("destroy", false) {
		t.Fatal("destroy --yes confirmation decision is incorrect")
	}
	if lifecyclePlanOnly("provision", lifecycleOptions{Plan: true}) == false {
		t.Fatal("provision plan flag ignored")
	}
	if needsPXEAck("bootstrap", true) || needsPXEAck("bootstrap", false) || needsPXEAck("start", false) || !needsPXEAck("start", true) {
		t.Fatal("PXE acknowledgement gate does not match apply/start boundary")
	}
}

func TestDestroyConfirmationPromptIsVisibleAndExact(t *testing.T) {
	var progress bytes.Buffer
	if !confirmDestroy(strings.NewReader("target-a\n"), &progress, "target-a") {
		t.Fatal("exact confirmation rejected")
	}
	if !strings.Contains(progress.String(), "Type target name \"target-a\"") {
		t.Fatalf("missing explicit prompt: %q", progress.String())
	}
	if confirmDestroy(strings.NewReader("target-a-extra\n"), io.Discard, "target-a") {
		t.Fatal("accepted non-exact confirmation")
	}
}

func TestBinaryPlanDigestDetectsPostConfirmationChange(t *testing.T) {
	path := t.TempDir() + "/plan.tfplan"
	before := []byte("immutable binary plan")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(before)
	if !binaryPlanMatches(path, digest) {
		t.Fatal("unchanged plan digest rejected")
	}
	if err := os.WriteFile(path, []byte("changed plan"), 0600); err != nil {
		t.Fatal(err)
	}
	if binaryPlanMatches(path, digest) {
		t.Fatal("accepted changed binary plan")
	}
}

func TestValidateTargetPlanRequiresFullTargetAndRejectsUnknownIdentity(t *testing.T) {
	target := Target{VMs: []VM{{ID: 930013, Name: "labfleet-issue13-test-01", Node: "pve1", Tags: []string{"labfleet", "disposable"}}, {ID: 930014, Name: "labfleet-issue13-test-02", Node: "pve1", Tags: []string{"labfleet", "disposable"}}}}
	p := []byte(`{"resource_changes":[{"address":"proxmox_virtual_environment_vm.labfleet[\"x\"]","type":"proxmox_virtual_environment_vm","mode":"managed","change":{"actions":["create"],"after":{"vm_id":930013,"name":"labfleet-issue13-test-01","node_name":"pve1","tags":"labfleet;disposable"}}}]}`)
	if _, err := validateTargetPlan(p, target, "infrastructure", false); err == nil {
		t.Fatal("accepted incomplete target plan")
	}
	var raw map[string]any
	_ = json.Unmarshal(p, &raw)
	rows := raw["resource_changes"].([]any)
	change := rows[0].(map[string]any)["change"].(map[string]any)
	change["after_unknown"] = map[string]any{"vm_id": true}
	b, _ := json.Marshal(raw)
	if _, err := validateTargetPlan(b, target, "infrastructure", false); err == nil {
		t.Fatal("accepted unknown identity")
	}
}

func TestDestroyPlanMustDeleteAllConfiguredVMs(t *testing.T) {
	target := Target{VMs: []VM{{ID: 930013, Name: "labfleet-issue13-test-01", Node: "pve1", Tags: []string{"labfleet", "disposable"}}}}
	p := []byte(`{"resource_changes":[{"address":"proxmox_virtual_environment_vm.labfleet[\"x\"]","type":"proxmox_virtual_environment_vm","mode":"managed","change":{"actions":["delete"],"before":{"vm_id":930013,"name":"labfleet-issue13-test-01","node_name":"pve1","tags":"labfleet;disposable"}}}]}`)
	got, err := validateTargetPlan(p, target, "infrastructure", true)
	if err != nil || len(got) != 1 || got[0].Action != "delete" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
