# Issue #3: disposable VM lifecycle validation

## Scope and tools

Validated against the isolated nested Proxmox API using OpenTofu 1.12.6 and
`bpg/proxmox` 0.114.0. Go checks used the installed system Go 1.26.0 toolchain.
Credentials were read from the operator-provided location outside the repository
and passed only through the process environment or API authorization header.
Endpoint, node, datastore details, complete snapshots, and raw plans/logs are
kept out of this report. The local lab test explicitly opted out of TLS
certificate verification; committed defaults continue to verify certificates.

No Proxmox host configuration, PXE service, OS image, guest configuration,
Kubernetes, or Ansible work was performed.

## Disposable resource

| Property | Observed value |
| --- | --- |
| VM ID | `930003` |
| Name | `labfleet-issue3-disposable-01` |
| Tags | `disposable;issue3;labfleet` |
| vCPU / RAM | 1 core / 512 MiB |
| Disk | One newly allocated blank 1 GiB SCSI disk |
| Installation media | None; no clone, cloud-init, or installed OS |
| BIOS / NIC | SeaBIOS / virtio |
| Boot order | `order=net0;scsi0` |
| Boot firmware | Running QEMU device tree reports `efi-virtio.rom` on `net0` |
| Runtime status | Running after apply; absent after destroy |

This establishes **network-boot capability**, not delivery of a PXE boot image
or installation of an OS. A PXE service is intentionally outside Issue #3.

## Observed lifecycle results

1. `tofu fmt -check -recursive`, `tofu init`, `tofu validate`, and seven mocked
   `tofu test` runs passed. Mocked tests do not access infrastructure.
2. The initial state was empty. A fresh creation plan contained exactly one
   resource: **1 add, 0 change, 0 destroy**. The live safety gate passed, including
   checking that the selected VM ID did not exist.
3. Applying that saved plan succeeded: **1 added, 0 changed, 0 destroyed**.
4. Live API reads verified the table above. Full pre-existing VM configurations
   were unchanged immediately after creation.
5. `tofu plan -detailed-exitcode` with the same local variables returned **0**;
   the only resource action was `no-op`. The ownership gate also passed this plan.
6. A destroy plan contained **0 add, 0 change, 1 destroy**, for VM `930003` only.
   The gate verified its live tag, identity, and matching provider ID / VM ID.
7. The requested `tofu destroy` command was run with confirmation enabled. Its
   newly generated confirmation plan was checked again for exactly that one
   deletion, and the live ownership gate was rerun immediately before confirming.
   It succeeded: **1 destroyed**. For normal operations, prefer applying a guarded
   saved destroy plan as described in the OpenTofu guide.
8. The live VM inventory returned to its original IDs. VM `930003` was absent,
   OpenTofu state listed no resources, and datastore volume IDs exactly matched
   the pre-test baseline: no test guest or disk was left behind.

## Protected resources and limitations

- All pre-existing VM configurations, identities, resource allocations, and
  running states matched the pre-test baseline. The coding-agent VM remained
  running and unchanged.
- Nested Proxmox node configuration and network configuration also matched the
  baseline. Network interface lists were compared by interface name because API
  array order is not stable.
- No unrelated infrastructure was accessed or modified. API writes were limited
  to the disposable VM lifecycle and read-only monitor queries on that VM.
- The safety checker rejects imports, replacements, unmanaged live tags, ID
  collisions (including containers), identity mismatches, and unsupported managed
  resource types. Tests exercise these rejection paths without touching the lab.
- A CLI check is not an authorization boundary or a transaction spanning Proxmox
  and OpenTofu. Direct API/OpenTofu use bypasses it; restrict token permissions,
  protect state, avoid concurrent writers, and never use targeted or
  refresh-disabled plans. Full PXE provisioning remains unimplemented.

See [the lifecycle guide](../infra/opentofu/README.md) for variables, credential
handling, ownership rules, and reproducible init/plan/apply/destroy steps.
