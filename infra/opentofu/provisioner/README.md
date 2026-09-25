# Persistent LabFleet provisioning infrastructure

This independent OpenTofu root creates new LabFleet-owned resources only:

- Simple SDN zone `labfleet` and VNet `lfpxe4` (alias `labfleet-provisioning`).
  Proxmox limits these IDs to eight characters. Simple SDN has no physical uplink;
  no subnet, host address, gateway, NAT, or DHCP is configured on Proxmox.
- A checksum-pinned Ubuntu 24.04 cloud image and a LabFleet-named NoCloud seed ISO.
- `labfleet-provisioner`, tagged `labfleet`, with separate control/provisioning
  NICs, 4 vCPU, 4 GiB RAM, and a 32 GiB disk. It boots its own disk, not PXE.

The existing management bridge is **referenced only by the new guest NIC**.
Nothing here manages that bridge, the node's management IP/default gateway, the
coding-agent VM, or another existing VM. Never import an existing resource into
these addresses. The persistent objects have `prevent_destroy`; the VM also has
Proxmox deletion protection. Disposable test guests remain in the parent root,
with separate state and the existing `fleetctl tofu-check` guard.

## Credentials and local inputs

Use `PROXMOX_VE_ENDPOINT` and `PROXMOX_VE_API_TOKEN` as in the parent guide. Copy
`example.tfvars` to ignored local inputs and verify the chosen VM ID, MACs, SDN
IDs and artifact names are unused. Never commit actual node/address/key inputs,
seed media, state, or plans. The supplied datastores must already support `iso`
and `import` (artifact storage) and VM images (disk storage). This root does not
change existing storage configuration.

## Automatic Ubuntu bootstrap without hypervisor SSH

Render NoCloud files with `go run ./provisioning/cmd/seedctl --config FILE` from
the repository root. The local JSON config needs `hostname` (LabFleet-prefixed),
`ownership_tag` (`labfleet`), `management_mac`, `provisioning_mac`,
`provisioning_cidr`, `username`, `ssh_public_key_file`, and `output`. Choose a
fresh output path: the renderer publishes the complete seed directory atomically
and refuses to replace an existing path.

The renderer configures control DHCP on `mgmt0` and a static, gateway-free
`prov0`. It installs `qemu-guest-agent`, `dnsmasq-base` (not the automatically
started generic dnsmasq service), iPXE, ISO extraction and capture tools. It
creates the service account/ownership marker and disables forwarding **inside
the new owned VM only**. Credentials are public-key-only; no private SSH key or
Proxmox token belongs in the seed.

Use an ISO authoring tool in a build workspace:

```sh
genisoimage -output /private/path/labfleet-provisioner-seed.iso \
  -volid cidata -joliet -rock /private/path/seed-directory
```

Set `seed_iso_path` to that file. OpenTofu uploads it as owned ISO content through
the Proxmox API; it does not use SSH or require snippets on the hypervisor. This
small seed media authoring step is separate from the target's network-only
Ubuntu installation. The provisioner cloud image is pinned to release
`20260911`, independently of the target's pinned Ubuntu Server 24.04.5 ISO.

## Three reviewed stages

**SDN activation applies all pending cluster SDN changes**, not just this root.
Do not execute the activation stage blindly or alongside another network writer.

1. Capture a private baseline of node network configuration, management
   address/gateway, VM inventory/configuration, and **all** SDN collections.
   Confirm no unrelated pending node-network or SDN changes exist.
2. With `stage = "network"`, initialize, validate, and save/review the plan.
   It must contain only the new owned zone and VNet. Apply that exact plan.
3. Compare desired SDN collections to `?running=1` views and the baseline.
   Controllers, fabrics, IPAM, DNS, prefix lists, route maps, and existing
   interfaces must be unchanged. The only pending objects may be the new owned
   zone and VNet. Abort on any unexplained change.
4. Set `stage = "activate"`; save/review/apply the plan containing only the
   applier creation. Verify the node's SDN zone/content/bridges API reports the
   VNet available with no physical ports. Recheck management configuration,
   API connectivity, and protected VM configuration/running state.
5. Set `stage = "provisioner"` and supply the seed ISO. Review the plan: only
   the owned image, seed, and unused VM ID may be created. Apply, discover its
   management DHCP address via the guest-agent API, attest its SSH host key
   through that API, and connect with strict SSH host-key checking.
6. Wait for `cloud-init status --wait`, then deploy the provisioning binary,
   verified artifacts, local runtime inputs, and dedicated systemd unit as
   described in [the provisioning guide](../../../docs/provisioning.md).

For each stage, use a saved plan, e.g.:

```sh
tofu init -input=false -lockfile=readonly
tofu validate
tofu plan -input=false -var-file=local.tfvars -out=stage.tfplan
tofu show stage.tfplan
tofu apply stage.tfplan
```

The experimental provider SDN applier is intentionally not triggered by arbitrary
later changes and does not activate networking on destroy. Future SDN changes
require the same complete pending-change review and an explicit application.
Do not move `stage` backwards or run destroy to clean up the disposable target;
the parent root owns that target independently. Retain this network/provisioner
for later LabFleet work unless a separately reviewed owned-resource teardown is
requested.
