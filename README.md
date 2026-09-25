# LabFleet

LabFleet is a Go-based systems platform for automated Linux and Kubernetes fleet
provisioning, diagnostics, observability, failure recovery, and distributed
data-ingest experiments. The repository is bootstrapped as a Go-first
control-plane project; infrastructure and machine-configuration workflows will
be added incrementally.

## Architecture

`fleetctl` is the operator-facing command-line entry point. `node-doctor` is a
small node diagnostics entry point. Shared application logic belongs in
`internal/`; OpenTofu manages infrastructure, Ansible configures machines, and
the provisioning and Kubernetes deployment directories hold their respective
workflows.

## Repository layout

- `cmd/fleetctl/`, `cmd/node-doctor/` — executable entry points
- `internal/` — private reusable Go packages
- `infra/opentofu/` — infrastructure-as-code
- `ansible/` — machine configuration
- `provisioning/` — provisioning workflows
- `deploy/kubernetes/` — Kubernetes deployment manifests
- `tests/` — integration and end-to-end tests
- `docs/` — project documentation
- `.github/workflows/` — pull-request and main-branch CI

## Status

Initial repository bootstrap. The command binaries are placeholders; no
infrastructure automation or node configuration is implemented yet.

## Developer usage

Requires Go 1.26 or newer and Make. Run `make fmt` to format Go sources,
`make test` to run tests, `make lint` for formatting and `go vet` checks, and
`make build` to build both binaries into `bin/`.
