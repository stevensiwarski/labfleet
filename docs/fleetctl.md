# `fleetctl` operator guide

`fleetctl` is the Go operator interface for discovering and validating LabFleet
resources and for coordinating the existing OpenTofu and Ansible workflows. It
does not replace the provider's ownership checks, `fleetctl tofu-check`, or the
Ansible safety preflight.

## Build and configure

Build with `go build -o bin/fleetctl ./cmd/fleetctl` (or `make build`). The
example at `configs/fleetctl.example.json` is a template, not a working lab
configuration. Copy it to a private location, set `repo_root` to this checkout,
select the correct OpenTofu root and provider variables file, and replace the
example VM identity and node. Keep the real config and variables outside Git;
they contain environment-specific details. Keep credentials in the existing
authorized environment/credential mechanisms, not these files. Set restrictive
permissions (for example `chmod 600`). Select a config with `--config PATH` or
`FLEETCTL_CONFIG`.

Every declared VM must have its exact name, VMID, Proxmox node, role and
LabFleet ownership tags in the configuration. The configured target is a
strict allowlist, not a way to adopt arbitrary existing machines. Lifecycle
operations require an explicit `--target`. Inspection commands require either
`--target NAME` or a valid `default_target` in the config; without either they
fail rather than guessing a fleet scope.

## Commands

```text
fleetctl version
fleetctl nodes [--config PATH] [--target NAME] [--output text|json]
fleetctl status [--config PATH] [--target NAME] [--output text|json]
fleetctl validate [--config PATH] [--target NAME] [--output text|json]
fleetctl provision --target NAME [--phase infrastructure|start|bootstrap] [--plan|--apply] [--pxe-ready]
fleetctl destroy --target NAME [--plan|--yes]
fleetctl tofu-check -plan PATH
```

`nodes` reports disposable LabFleet-owned VMs discovered through Proxmox, with
name, VMID, role, and running state; JSON also includes tags. `status` summarizes managed VM count, Kubernetes readiness,
API reachability, Cilium and CoreDNS. `validate` reports ownership and
environment checks. JSON output uses a command/status/exit_code envelope with
structured data and checks; use `--output json` for automation. Text output is
intended for operators, not machine parsing.

Provisioning is deliberately phased. The default `provision` invocation is a
checked plan and does not apply changes. `--apply --phase infrastructure`
applies only after OpenTofu plan ownership checks. `--apply --phase start`
starts the configured guests only after the operator has prepared the existing
isolated PXE/autoinstall service and explicitly passes `--pxe-ready`. `fleetctl`
does not configure or activate PXE itself. `--apply --phase bootstrap` invokes
the existing guarded Ansible host/cluster workflow; ensure its prerequisites
and target readiness are satisfied first. Review every plan and follow the
existing [provisioning](provisioning.md), [host configuration](ansible-host-configuration.md),
and [Kubernetes bootstrap](kubernetes-bootstrap.md) runbooks.

Destroy requires an explicit configured target. By default it presents the
checked destruction plan and requires interactive confirmation of the exact
target name. `--yes` is the deliberate non-interactive confirmation. Even
then, provider ownership checks must pass; unmanaged or ambiguous resources
fail closed. Use `--plan` to inspect without applying. Never target the coding
agent, Proxmox management system, or persistent provisioner: these are protected,
not disposable targets. Do not use the retained cluster for CLI lifecycle tests.

## Exit codes and safety

Successful operations return `0`. Usage/configuration errors return `2`,
lifecycle/tool execution failures return `1`, unsafe or unconfigured targets return
`4`, degraded inspection or failed validation returns `3` (including unavailable
Proxmox inventory), and interruption returns `130`.
`WARN` checks alone do not cause a failure; missing required cluster health does.
JSON errors include a classification and
message; wrapped tool failures preserve the tool exit status where available.
Do not treat a nonzero exit as permission to retry destructive work blindly:
inspect provider state and the reported phase before resuming.

OpenTofu and Ansible run with bounded timeouts and captured output/status.
Credentials are consumed from the operator's existing private provider and
Kubernetes configuration, not embedded in the CLI config or source. Avoid
publishing command output that may contain private environment information.

### Provider and local configuration

Use the existing `PROXMOX_VE_ENDPOINT`, `PROXMOX_VE_API_TOKEN`, and optional
`PROXMOX_VE_INSECURE=true` environment variables. HTTPS certificate verification
is enabled by default. An insecure override is explicit and must agree with
the OpenTofu `tls_insecure` variable. A non-null `proxmox_endpoint` in the saved
plan must match the API endpoint used for ownership checks. Tokens are never
CLI arguments or configuration-file fields.

The JSON config must be a private regular nonsymlink file. `repo_root` is the
checkout; `work_dir` is an existing absolute directory with mode `0700` for
private saved plans. Each target has `kind` (`vm` or `cluster`), `tofu_root`,
`vars_file`, and an exact `vms` array. Variables files must be regular,
nonsymlink, private files. Roots are restricted to the reviewed disposable VM
and cluster roots; the persistent provisioner root is not eligible. The cluster
target follows the existing fixed six-node identity contract. A VM-only target
cannot reuse those six identities. Targets cannot overlap.

`kubeconfig` selects a private admin configuration; if omitted, `KUBECONFIG`
provides one absolute path. Cluster health is degraded if it is absent or
unusable. `tofu`, `ansible_playbook`, and `kubectl` select executable names or
paths; defaults use PATH. `timeout_seconds` bounds lifecycle subprocesses
(default 1800; maximum 3600). Inspection subprocesses are bounded to 30 seconds.

Lifecycle operations refuse ambient `TF_VAR_*`, `TF_CLI_ARGS*`, non-default
`TF_WORKSPACE`, `TF_DATA_DIR`, `TF_CLI_CONFIG_FILE`, and `ANSIBLE_*` settings,
which could change state, tool arguments, or Ansible configuration. Use the
private target variables file and the repository's Ansible configuration instead.

### Provisioning sequence

1. Prepare a private target config and OpenTofu variables with `started=false`.
2. Run `fleetctl provision --target NAME --plan`; inspect the action summary and
   saved private plan. Planning may initialize provider caches and refresh state
   observations, but does not apply infrastructure changes.
3. Run `fleetctl provision --target NAME --apply` to create only the explicitly
   scoped stopped VMs. This generates and checks a fresh saved plan.
4. Prepare the existing isolated PXE service using the provisioning runbook.
   This operator boundary is intentional: the CLI does not modify the shared
   persistent provisioner. Run `--phase start --plan`, then
   `--phase start --apply --pxe-ready` to permit only the stopped-to-running change.
5. Allow installation and QGA readiness. Keep the private variables' `started`
   value aligned with the running guests so the bootstrap plan is a no-op.
6. Configure `discovery_vars` as the existing private JSON discovery inputs and
   `inventory` as `<cluster_private_dir>/inventory.json`. Run
   `fleetctl provision --target NAME --phase bootstrap --apply`.

Bootstrap invokes `discover-cluster.yml`, verifies the resulting inventory's
exact identity and role scope, then invokes `bootstrap-cluster.yml` and
`validate-cluster.yml` from `ansible/`. Host preparation remains owned by the
existing bootstrap playbook. No shell-built commands or `--limit` shortcuts
are used. The independent Ansible validation creates and removes temporary
network-test workloads; the separate `fleetctl validate` command is read-only.

### Ownership and destruction

Eligibility combines reserved VMID range, expected LabFleet naming, ownership
and disposable tags, exact configured identity/role tags, selected root and
state relationship, and live Proxmox configuration attestation. Protected
names, IDs, tags, and protection flags are refused. All managed changes and
prior state resources must remain within the selected target. Missing members,
unknown identities, replacement plans, and unrelated resources fail closed.

The existing `internal/tofusafety` checker is used without weakening its rules.
It runs on the saved plan and again before apply. The saved binary plan is
checked for changes across confirmation. `destroy --yes` skips only the
interactive prompt, never ownership or scope checks. `destroy --plan` never
applies. A redirected stdin without `--yes` cannot silently confirm destruction.

### Version and implementation

Development builds report `dev`, with commit and build time `unknown`. Release
metadata can be injected explicitly, for example:

```sh
go build -ldflags '-X github.com/stevensiwarski/labfleet/internal/fleetctl.Version=v0.1.0' -o bin/fleetctl ./cmd/fleetctl
```

`cmd/fleetctl` owns process dispatch and cancellation. `internal/fleetctl`
contains configuration, provider reads, cluster inspection, plan decisions,
lifecycle orchestration, and structured output in separate files.
`internal/runner` owns bounded shell-free subprocess execution, separate capped
stdout/stderr, exit status, and process-group cancellation. Existing
`internal/tofusafety` remains the shared OpenTofu ownership guard.

### Limits

This Linux MVP coordinates explicit phases, not a resumable workflow engine.
It does not activate PXE, automatically upgrade clusters, repair failed nodes,
or implement later roadmap work. An interrupted apply can leave partial state;
inspect OpenTofu and provider state before retrying. Status reports observed
pod readiness rather than a complete network health test. Provisioner reporting
is VM power state only, not PXE service state. Plans and inventories are private
artifacts and must not be published. Existing cluster limitations, including
the non-HA API endpoint, remain unchanged.

## Issue #13 validation evidence

Live validation used the retained six-node cluster for read-only inspection
and a separate small stopped VM for lifecycle operations. It did **not**
reinstall the cluster or activate the shared PXE service.

| Check | Observed result |
| --- | --- |
| `version`, text and JSON | Exit 0; `dev`, commit/build time `unknown` |
| `nodes`, text and JSON | Exit 0; exactly three control planes and three workers; all running; protected infrastructure excluded |
| `status`, text and JSON | Exit 0; 3/3 control planes and 3/3 workers Ready; API reachable; Cilium agents/operator and CoreDNS passed |
| `validate`, text and JSON | Exit 0; all 17 checks passed |
| Retained-cluster provision plan | Six no-op actions, zero changes |
| Dedicated VM provision | Exit 0; exactly one stopped Issue #13-owned VM created and independently attested |
| Dedicated VM destroy plan | Exactly that one VM selected for deletion |
| Non-interactive destroy without `--yes` | Exit 4; refused without applying |
| Dedicated VM destroy with `--yes` | Exit 0; VM and disk independently confirmed absent |
| Protected/unconfigured target requests | Exit 4; refused before lifecycle tools |
| Legacy `tofu-check -plan` | Safe, zero-change retained-cluster plan; existing tests preserved |

Before/after independent `kubectl` checks found six Ready nodes and all 33
system pods Running with every container Ready. The cluster namespace UID was
unchanged. All eight pre-existing VM configurations and Proxmox node/network
configuration matched the captured baseline. The provisioner and coding-agent
VMs remained running and outside the disposable inventory.

Local validation passed Go build, test, race, vet, and all Make targets; all
three OpenTofu roots' formatting, initialization, validation, and 25 mocked
tests; five Ansible playbook syntax checks, 35 offline tests, and lint with zero
warnings or failures. New tests cover configuration, protected ownership,
provider fixtures, degraded Kubernetes states, JSON/exit codes, exact plan
scope, confirmation, environment injection, inventory SSH settings, and runner
cancellation/timeout/process-group cleanup.

The live create initially refused equivalent `tofu show -json` output whose
object-key ordering differed. Comparison now checks JSON semantics while
retaining binary saved-plan digest checks; a regression test covers this.
Proxmox's aggregate resource list briefly lagged successful deletion; subsequent
independent inventory and storage checks confirmed complete cleanup. Tests also
cover numeric Proxmox protection flags and deterministic first-control-plane
inventory selection.

Full PXE installation and cluster bootstrap **through the new CLI** were not
rerun: that would unnecessarily alter the retained cluster. The existing plays
remain unchanged, their offline suites pass, and CLI bootstrap scope/SSH input
decisions are unit-tested. The live end-to-end operation demonstrated the
permitted blank-VM OpenTofu lifecycle, not a fresh OS installation.
