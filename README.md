# LabFleet

LabFleet is a Go-based systems platform for automated Linux and Kubernetes fleet
provisioning, diagnostics, observability, failure recovery, and distributed
data-ingest experiments. The repository is bootstrapped as a Go-first
control-plane project; infrastructure and machine-configuration workflows will
be added incrementally.

## Architecture

The intended architecture uses `fleetctl` as the operator-facing command-line
entry point and `node-doctor` for node diagnostics. Shared application logic
belongs in `internal/`. OpenTofu will manage LabFleet-owned VM lifecycles,
Ansible will configure those machines, and provisioning workflows will
coordinate these steps. Kubernetes manifests will describe lab workloads;
observability configuration and distributed data-ingest experiments have
separate deployment directories. These are planned responsibilities, not
implemented capabilities.

### Infrastructure boundaries

The coding-agent VM is bootstrap/control infrastructure, not a managed fleet
node. It, the nested Proxmox management instance, the outer/production Proxmox
environment, and production infrastructure must not be modified, reprovisioned,
destroyed, rebooted, or intentionally disrupted by LabFleet work.

Only resources explicitly tagged or identified as LabFleet-managed may be
modified. Ownership must be verified before any destructive action; if it
cannot be established, stop and request human review. See [AGENTS.md](AGENTS.md)
for the operating rules. This bootstrap defines and accesses no infrastructure.

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

Initial repository bootstrap. The command binaries are placeholders; no
infrastructure automation, node diagnostics/configuration, observability stack,
or ingest workloads are implemented yet. Unit tests cover placeholder messages;
integration and end-to-end tests are deferred until those capabilities exist.

## Developer usage

Requires Go 1.26 or newer and Make. Run `make fmt` to format Go sources,
`make test` to run tests, `make lint` for formatting and `go vet` checks, and
`make build` to build both binaries into `bin/`.
