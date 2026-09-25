# Disposable Proxmox machines

This root creates **blank VMs only** in an isolated nested Proxmox lab. It does
not clone an image, install an OS, configure the hypervisor, create a bridge,
provide DHCP/PXE services, or configure guests. The coding-agent VM and nested
Proxmox management instance are bootstrap/control infrastructure, not fleet
members. Never import them, attach their storage, or change their configuration.

## Prerequisites and provider

- OpenTofu 1.11+ (CI uses 1.12.6), Go 1.26+, and an isolated nested Proxmox node.
- Existing image-capable datastore and isolated network bridge.
- API-token permissions for the intended disposable VM IDs, disk allocation,
  inventory reads, and VM configuration. Use the narrowest Proxmox ACLs possible.
- `bpg/proxmox` **0.114.0**, pinned with a committed dependency lockfile. This
  provider supports blank disks, explicit VM IDs, tags, and network boot through
  the Proxmox API. No SSH configuration or credentials are required.

Nested virtualization must expose hardware virtualization to Proxmox. Available
RAM, CPU, disk capacity, and CPU features remain bounded by the nested host. The
conservative `qemu64` CPU model avoids assuming host-specific CPU features;
these small guests are not a production performance model.

Destroy stops these disposable blank guests rather than waiting for an absent
OS to handle ACPI shutdown. It does not purge backup-job configuration or remove
unreferenced disks; this root creates only its own attached blank disk.

## Configuration and credentials

Copy `example.tfvars` to ignored `local.tfvars` and replace its placeholder node,
storage, bridge, and ID range after checking the live inventory. Never commit
that local file. The example contains no real endpoint, node, or credentials.

Inject these environment variables through your approved credential mechanism:

- `PROXMOX_VE_ENDPOINT`: HTTPS base URL, e.g. `https://proxmox.example.invalid:8006/`
  (no `/api2/json` suffix).
- `PROXMOX_VE_API_TOKEN`: `user@realm!token-id=secret`, **without** the
  `PVEAPIToken=` header prefix. Do not place the token in shell history, tfvars,
  command arguments, reports, or the repository.
- `PROXMOX_VE_INSECURE=true`: optional, explicit lab-only TLS bypass for the Go
  ownership checker. If used, also set `tls_insecure = true` in local tfvars for
  the provider. Prefer trusted TLS certificates; both defaults verify TLS.

The provider endpoint can alternatively be supplied as `proxmox_endpoint` (or
`TF_VAR_proxmox_endpoint`). The checker still needs `PROXMOX_VE_ENDPOINT` set to
the **same** endpoint. Never point the provider and checker at different clusters.

| Variable | Meaning / default |
| --- | --- |
| `proxmox_node` | Required existing nested node name |
| `datastore_id` | Required existing image-capable storage |
| `network_bridge` | Required existing isolated bridge |
| `first_vm_id` | Required independently reserved free ID; range 900000..999999 |
| `vm_name_prefix` | `labfleet-test`; must start `labfleet-`; suffix `-01`, `-02`, etc. |
| `vm_count` | 1; integer 1..20 |
| `vcpu` | 1 core per VM |
| `memory_mib` | 512 MiB per VM |
| `disk_gib` | 4 GiB blank SCSI disk per VM |
| `network_boot` | true; SeaBIOS boot order `net0`, then `scsi0` |
| `started` | false; opt in to starting the guest only on the isolated lab bridge |
| `ownership_tag` | Must be `labfleet`; validation rejects any replacement |
| `additional_tags` | Empty set; extra tags never replace `labfleet` |
| `proxmox_endpoint` | null, reads provider environment variable |
| `tls_insecure` | false; lab-only explicit TLS verification bypass |
| `vm_mac_addresses` | Empty; optionally one unique `02:` MAC per VM for PXE DHCP reservations |
| `disk_serial_prefix` | null; optionally `labfleet-` plus 1..4 lowercase alphanumeric characters; VM ID is appended |

Network boot capability is not an installed OS or a successful PXE installation.
SeaBIOS with a virtio NIC can attempt PXE; a PXE server, DHCP, boot artifacts,
Kubernetes, and Ansible are intentionally outside this issue.

For the Issue #4 provisioning service, see [the provisioning guide](../../docs/provisioning.md).
Use an explicitly isolated existing bridge, an allowlisted fixed MAC, and a disk
serial matched by autoinstall. Override the small blank-VM defaults with at least
8 GiB RAM and 20 GiB disk for the Ubuntu installer. Do not attach a provisioning
service to the management bridge or create a new host network as an implicit step.

## Ownership and state safety

Every VM has the exact `labfleet` tag, a `labfleet-` name, and an explicit high
VM ID. The high ID range is a repository convention, **not proof of ownership**.
Check that the chosen IDs are globally free before creation. There are no import,
clone, host-management, or existing-VM adoption blocks in this configuration.
Do not run `tofu import`, add import blocks, or manually insert objects into state.

**Tags alone do not protect an imported VM.** Before every apply or destroy,
run `fleetctl tofu-check` on a fresh complete saved plan. It reads live inventory
without changing Proxmox, rejects unmanaged resources and imports/replacements,
checks create IDs for collisions, and requires existing resources to already
have the expected identity and live `labfleet` tag. A plan that merely proposes
adding the tag to an unmanaged VM is not ownership proof.

The checker is an operator safety gate, not a Proxmox authorization boundary:
direct OpenTofu/API operations can bypass it. Restrict token ACLs and state access;
never use a broad administrative token for unattended production operations.
Inventory can change after a check, so do not run competing writers, use state
locking, and apply only the exact plan just reviewed. Stop on ownership ambiguity.

Keep state, state backups, JSON plans, local tfvars, and logs private. These may
contain sensitive environment values even when the API token is environment-only.
Git ignores them, but ignore rules are not access control. Use `umask 077`; do not
commit generated state or plan artifacts. A separate state directory is required
for each independently managed fleet. Losing state is not permission to import
or delete an arbitrary VM.

## Safe lifecycle

From the repository root, build the checker, then work in this directory:

```sh
make build
cd infra/opentofu
umask 077
tofu fmt -check -recursive
tofu init -input=false
tofu validate
tofu test                 # mocked provider; no live infrastructure
tofu plan -var-file=local.tfvars -out=create.tfplan
tofu show create.tfplan   # inspect locally; do not publish sensitive values
tofu show -json create.tfplan > create.tfplan.json
../../bin/fleetctl tofu-check -plan create.tfplan.json
tofu apply create.tfplan
```

Before applying, confirm the plan contains only the intended blank disposable
VM creation, no changes to bootstrap or unrelated VMs, and no unexpected disk,
network, or power operations. Record existing VM configurations privately before
the test and compare them after creation and teardown. Verify the managed VM's
name, VM ID, `labfleet` tag, `boot: order=net0;scsi0`, blank disk, and requested
CPU/RAM through the live Proxmox API. Use `started=true` only for an intentional
boot test. Check idempotence with a fresh
`tofu plan -input=false -var-file=local.tfvars -detailed-exitcode` (exit 0).

For teardown, generate and guard a destroy plan using the same state and inputs:

```sh
tofu plan -destroy -var-file=local.tfvars -out=destroy.tfplan
tofu show destroy.tfplan
tofu show -json destroy.tfplan > destroy.tfplan.json
../../bin/fleetctl tofu-check -plan destroy.tfplan.json
tofu apply destroy.tfplan
```

Applying the saved destroy plan performs teardown without the replan performed
by `tofu destroy`. If anything changes, regenerate, recheck, and apply a new saved
destroy plan rather than bypassing the gate. Never use targeted or
refresh-disabled plans to bypass the gate. Verify the disposable VM is absent,
all pre-existing VM configurations are unchanged, and no test guest remains.
If creation partially fails, inspect state and live ownership before attempting
cleanup; never delete by a name prefix alone.
