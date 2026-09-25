# LabFleet

LabFleet is a Go-based systems platform for automated Linux and Kubernetes fleet
provisioning, diagnostics, observability, failure recovery, and distributed
data-ingest experiments. The repository is bootstrapped as a Go-first
control-plane project; infrastructure and machine-configuration workflows will
be added incrementally.

## Architecture

The intended architecture uses `fleetctl` as the operator-facing command-line
entry point and `node-doctor` for node diagnostics. Shared application logic
belongs in `internal/`. OpenTofu manages disposable LabFleet-owned VM lifecycles;
Ansible will configure those machines, and provisioning workflows will
coordinate these steps. Kubernetes manifests will describe lab workloads;
observability configuration and distributed data-ingest experiments have
separate deployment directories. Beyond the blank VM lifecycle and ownership
checker, these are planned responsibilities, not implemented capabilities.

### Infrastructure boundaries

The coding-agent VM and nested Proxmox management instance are bootstrap/control
infrastructure and are not part of the managed LabFleet fleet.

Only resources explicitly tagged or identified as LabFleet-managed may be
modified. Ownership must be verified before any destructive action; if it
cannot be established, the operation must stop.

See [AGENTS.md](AGENTS.md) for the operating rules.

## Repository layout

- `cmd/fleetctl/`, `cmd/node-doctor/` — executable entry points
- `internal/` — private reusable Go packages
- `infra/opentofu/` — infrastructure-as-code
- `ansible/` — machine configuration
- `provisioning/` — provisioning workflows
- `deploy/kubernetes/` — Kubernetes deployment manifests
- `deploy/observability/` — planned metrics, dashboards, and alerting configuration
- `deploy/ingest/` — planned distributed data-ingest experiment deployments
- `tests/` — integration and end-to-end tests
- `docs/` — project documentation
- `.github/workflows/` — pull-request and main-branch CI

## Status

The repository includes a minimal Go scaffold and an OpenTofu lifecycle for
disposable blank Proxmox VMs. `fleetctl tofu-check` validates a saved plan against
live ownership before applying it; `node-doctor` remains a placeholder. See
[the OpenTofu guide](infra/opentofu/README.md) for credentials, variables, safety,
and lifecycle steps. PXE services, OS installation, guest configuration,
Kubernetes, observability, and ingest workloads are not implemented.

## Developer usage

Requires Go 1.26 or newer and Make. Run `make fmt` to format Go sources,
`make test` to run tests, `make lint` for formatting and `go vet` checks, and
`make build` to build both binaries into `bin/`.
