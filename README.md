# LabFleet

LabFleet is a Go-based systems platform for automated Linux and Kubernetes fleet
provisioning, diagnostics, observability, failure recovery, and distributed
data-ingest experiments. Infrastructure provisioning, host configuration, and
Kubernetes bootstrap are implemented; diagnostics, observability, ingest, and
chaos/failure testing remain future roadmap work.

## Architecture

The implemented path is:

```text
OpenTofu -> guarded LabFleet-owned VM lifecycle
         -> isolated PXE/autoinstall -> Ubuntu 24.04
         -> Ansible host configuration -> containerd/Kubernetes prerequisites
         -> kubeadm: 3 control planes + 3 workers
         -> Cilium networking -> cluster validation (including CoreDNS)
```

`fleetctl tofu-check` guards infrastructure plans using live ownership evidence.
Go provisioning services and Ansible playbooks automate the node and cluster
workflows; reusable Go logic lives in `internal/`. Node diagnostics through
`node-doctor`, observability, distributed ingest, chaos/failure testing, and later
roadmap capabilities remain planned, not implemented.

### Infrastructure boundaries

The coding-agent VM and nested Proxmox management instance are bootstrap/control
infrastructure and are not part of the managed LabFleet fleet.

Only resources explicitly tagged or identified as LabFleet-managed may be
modified. Ownership must be verified before any destructive action; if it
cannot be established, the operation must stop.

See [AGENTS.md](AGENTS.md) for the operating rules.

## Repository layout

- `cmd/fleetctl/` — CLI scaffold and implemented ownership-plan checker
- `cmd/node-doctor/` — placeholder for planned node diagnostics
- `internal/` — private reusable Go packages
- `infra/opentofu/` — guarded VM lifecycle, persistent provisioner, and six-node cluster infrastructure
- `ansible/` — host configuration, kubeadm bootstrap, Cilium installation, and cluster validation
- `provisioning/` — isolated PXE/autoinstall services and Ubuntu provisioning workflows
- `deploy/kubernetes/` — reserved for future lab workload manifests; cluster bootstrap lives in `ansible/`
- `deploy/observability/` — planned metrics, dashboards, and alerting configuration
- `deploy/ingest/` — planned distributed data-ingest experiment deployments
- `tests/` — integration and end-to-end tests
- `docs/` — project documentation
- `.github/workflows/` — pull-request and main-branch CI

## Status

- [Guarded disposable Proxmox VM lifecycle](infra/opentofu/README.md) is implemented.
- [Isolated unattended Ubuntu PXE provisioning](docs/provisioning.md) is implemented and live validated.
- [Idempotent Ansible host configuration](docs/ansible-host-configuration.md) is implemented and live validated.
- [Reproducible Kubernetes bootstrap](docs/kubernetes-bootstrap.md) is implemented
  and live validated: **3 control planes + 3 workers**, validated Cilium and
  CoreDNS, zero-change bootstrap reruns, and a demonstrated full cluster
  destroy/recreate cycle. The final cluster is retained; its cp-01 API endpoint
  is not itself highly available.

`node-doctor`, observability, distributed ingest, and chaos/failure testing remain
future roadmap work. The linked guides contain validation evidence, operating
procedures, and limitations.

## Developer usage

Requires Go 1.26 or newer and Make. Run `make fmt` to format Go sources,
`make test` to run tests, `make lint` for formatting and `go vet` checks, and
`make build` to build both binaries into `bin/`.
