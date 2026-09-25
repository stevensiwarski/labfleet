# Issue #7: automated host configuration and validation

## Scope

This prepares owned, disposable Ubuntu 24.04 guests for later Kubernetes work:
base packages and updates, key-only SSH, systemd-timesyncd, kernel modules,
sysctls, swap handling, and containerd with CRI/systemd cgroups. It does **not**
install kubeadm/kubelet, select a Kubernetes version, initialize/join a cluster,
or introduce cluster credentials. See the [operator guide](../ansible/README.md).

The original draft was blocked by a read-only provisioner restriction, absent
disposable node, missing become path, and isolated-network-only connectivity.
The subsequent Issue #7 request explicitly authorized using the existing owned
provisioner and attaching a disposable guest to the existing management network.
No new branch, PR, bridge, routing service or provisioning architecture was created.

## Target topology and administrative access

- Target: `labfleet-issue7-test-01`, VM **930007**, tags
  `labfleet;disposable;issue7`, Ubuntu **24.04.5 LTS**.
- Fresh blank 24 GiB disk, 4 vCPU, 16 GiB RAM; guarded OpenTofu lifecycle.
- NIC0: existing management bridge, installed name `mgmt0`, DHCP default route,
  DNS, repository and NTP access. Only the guest attachment was configured.
- NIC1: existing isolated `lfpxe4` provisioning network, installed name `prov0`.
  Firmware boots `net1;scsi0`; iPXE `BOOTIF` selects the provisioning MAC.
  DHCP supplies no gateway/DNS, and netplan explicitly rejects routes/DNS on
  this interface. The provisioner is not a router.
- The reusable seed's explicit `passwordless_sudo` option gives existing `fleet`
  noninteractive sudo in a root-owned mode-0440 file checked by `visudo`.
  Root-equivalent local privilege is necessary for Ansible's apt, systemd,
  modules, sysctls, swap and privileged file operations. This is confined to
  opted-in disposable nodes, not the provisioner/controller. Password SSH and
  remote root access remain disabled.
- The dual-NIC profile installs QEMU guest agent. The authenticated Proxmox API
  provides the management address and SSH public host key, pinned before direct
  key-only SSH. No host-key-checking bypass or copied private key is used.

Single-NIC and no-passwordless-sudo defaults remain unchanged. Real inventory,
addresses, credentials, keys, state, captures and raw logs stay outside Git.

## Live preflight and safety

The inventory contains only the disposable node, not the coding-agent,
provisioner or Proxmox management. Ansible's read-only preflight completed with
`ok=9 changed=0 unreachable=0 failed=0 skipped=0 rescued=0 ignored=0`.
It checked explicit disposable opt-in, API ownership/name/tags, absence of
protection/control tags, and guest UUID/MAC/address/OS identity. The VM name alone
was not accepted as ownership evidence.

Before configuration, direct pinned-key SSH and `sudo -n` succeeded. DNS resolved
the Ubuntu archive, HTTPS fetched the Noble repository's `InRelease` with HTTP
200, and `timedatectl` reported `NTPSynchronized=yes`. The sole default-route
interface was `mgmt0`; `prov0` had no default route. PXE DHCP was bound to `prov0`.

Offline tests execute actual assertion tasks against synthetic fixtures. They
reject protected IDs/groups, unowned tags, absent UUID/MAC, deletion protection,
wrong guest identity/OS, missing disposable opt-in, and local/root connections.
The full play rejects a protected fixture before any API request/configuration
role, bootstrap-only inventory matches no hosts, and skipping the primary
`always` preflight is rejected. No protected/unmanaged host was deliberately
configured, including in check mode.

## Failures found and corrected

1. A stale infinite DHCP lease belonged to the removed Issue #4 target. Issue #7
   received a distinct unused isolated reservation instead of overwriting the
   old lease or widening DHCP scope.
2. Identity collection now explicitly requests the `virtual` fact collector;
   `hardware` and `network` alone do not supply virtualization identity.
3. Current Noble repositories offer containerd 2.2.1. The role now supports the
   1.7/config-v2 and 2.2/config-v3 layouts, checking the appropriate CRI plugins.
   Version 3 uses separate runtime/images plugins and `pinned_images.sandbox`,
   following [upstream v2.2.1 documentation](https://github.com/containerd/containerd/blob/v2.2.1/docs/cri/config.md).
4. systemd 255 returns status 1 for an empty filtered swap-unit JSON list. The
   role accepts only `rc=1`, stdout `[]`, empty stderr; other errors fail closed.
   Enabled custom swap units still fail rather than being silently disabled.
5. Live execution exposed regex backreference escaping in version selection.
   Selection now uses `regex_findall`, and tests execute selection plus validation.
6. Generated sudo late-commands use YAML argv, avoiding nested shell/JSON escape
   ambiguity. A decoded-YAML regression test checks the real generated command.
   Tests also cover cross-VM/case-insensitive MAC reuse and CRI output spacing.

Initial development attempts are retained honestly, not relabeled as successful
fresh runs (columns are `ok/changed/unreachable/failed/skipped/rescued/ignored`):

| Attempt | Recap | Outcome |
|---|---|---|
| Initial | `38/12/0/1/3/0/0` | Stopped at empty swap-unit query |
| First repair | `41/2/0/1/5/0/0` | Stopped at containerd version parser |
| Corrected completion | `48/2/0/0/5/0/0` | Successful full configuration |
| Identical second run | `47/0/0/0/5/0/0` | Zero unnecessary changes |

## Final fresh-node evidence

The target was destroyed through the ownership-checked lifecycle and recreated
with a blank disk, then PXE-installed again. The final playbook ran unchanged
twice against that fresh node, followed by configured-host check mode:

| Final run | ok | changed | unreachable | failed | skipped | rescued | ignored |
|---|---:|---:|---:|---:|---:|---:|---:|
| Fresh first | 53 | 16 | 0 | 0 | 3 | 0 | 0 |
| Identical second | 47 | **0** | 0 | **0** | 5 | 0 | 0 |
| Check mode | 32 | 0 | 0 | 0 | 20 | 0 | 0 |

Live results:

- Base packages/updates completed; SSH remained reachable with pinned public-key
  authentication and noninteractive sudo. Effective `sshd -T` confirmed
  `permitrootlogin no`, `passwordauthentication no`, `pubkeyauthentication yes`.
- Actual runtime: **containerd 2.2.1**, service **enabled/active**, CRI images and
  runtime plugin health assertions passed. Effective config dump confirmed
  `disabled_plugins = []` and `SystemdCgroup = true`.
- `overlay` and `br_netfilter` loaded and persisted in
  `/etc/modules-load.d/labfleet-kubernetes.conf`.
- `net.ipv4.ip_forward`, `net.bridge.bridge-nf-call-iptables`, and
  `net.bridge.bridge-nf-call-ip6tables` all verified **1**; persistent sysctl file
  installed by the role. These are guest-only settings.
- No active swap; `/swap.img` entry commented in fstab, no enabled swap units.
- systemd-timesyncd **enabled/running**, `NTPSynchronized=yes`.
- No pending reboot marker. No kubeadm init/join or Kubernetes cluster created;
  existing cluster-marker guards passed.

## Cleanup and protected infrastructure

Both destroy plans contained only the owned disposable VM and passed
`fleetctl tofu-check`. Final independent checks confirmed VM **930007 absent**,
its matching disks absent, and disposable OpenTofu state empty. Persistent
LabFleet network and provisioner remain intact.

The provisioner's original configuration and binary were restored byte-for-byte
from saved backups. The final reconciliation caught a still-active PXE service
and stopped it: **inactive/disabled**, **zero provisioning DHCP sockets**.
The test capture is inactive; provisioner IPv4 forwarding remains **0**. The
management capture contained **zero DHCP replies sourced by the provisioner**;
replies from the existing management DHCP server are not isolation failures.

Coding-agent and persistent VM configurations/running states, Proxmox node
configuration, and management interfaces/address/gateway matched their baselines.
API interface lists were compared by interface name to avoid false differences
from list ordering. No coding-agent machine configuration, Proxmox management,
unmanaged guest or management bridge was modified. The provisioner was never
an Ansible target. Private logs/captures remain diagnostic artifacts, not live
disposable infrastructure.

## Acceptance matrix

| GitHub Issue #7 acceptance criterion | Result |
|---|---|
| Inventory supports LabFleet nodes | PASS — dedicated group and private inventory used live |
| Coding-agent excluded by default | PASS — explicit group scope and protected fixture rejection |
| Base configuration completes successfully | PASS — final fresh first run succeeded |
| Containerd installs successfully | PASS — 2.2.1 enabled/active, CRI healthy |
| Kubernetes prerequisites configured | PASS — modules/sysctls/swap verified without cluster bootstrap |
| Running twice produces no unnecessary changes | PASS — identical fresh second run changed=0, failed=0 |
| No secrets in committed inventories | PASS — synthetic examples; real inputs outside Git |
| Example inventory provided | PASS — non-deployable example with opt-in disabled |
| Bootstrap targeting prevented/guarded | PASS — inventory/API/guest checks and offline negative tests |

All live acceptance criteria are satisfied. PR #11 can be marked ready after
publication and successful GitHub Actions; it must not be merged by this task.

## Local validation

With Ansible Core 2.20.3 / Ansible Lint 26.3.0 in an isolated temporary toolchain:

- Example inventory export and playbook syntax checks: PASS.
- Production-profile Ansible lint: PASS.
- Offline safety, role and rendered-seed contracts: **14 test methods**, PASS.
- `go build ./...`, `go test ./...`, `go test -race ./...`: PASS.
- `make fmt`, `make build`, `make test`, `make lint`: PASS.
- Both OpenTofu roots: recursive formatting, input-disabled/readonly-lockfile
  initialization, validate and tests: PASS (**18 disposable + 5 provisioner**).
- `git diff --check` and candidate credential/private-address/private-key scan:
  PASS. The header-only negative SSH-key fixture is not private key material.
- Independent read-only source review found no blocking issue.

## Limitations

- This is node preparation, not proof of Kubernetes cluster operation.
- The lab's private API uses an explicit TLS-verification override; production
  should trust its CA. SSH host keys remain strictly verified.
- Idempotence is measured with identical inputs inside the apt cache window.
  Newly available security updates, expired metadata and actual drift may
  legitimately produce changes on later runs.
- Pristine check mode cannot install packages or validate an absent runtime;
  use full runs for acceptance. A containerd 2.x preview is not a detected version.
- Containerd 1.7 and 2.2 are supported; other families require explicit validation.
- Custom SSH `Match` blocks and nonstandard swap generators require site review.
- No automatic node reboot is performed. A pending reboot is reported separately.
- Existing GitHub Actions covers Go/OpenTofu; Ansible checks run locally in the
  pinned isolated controller environment, not in CI.
