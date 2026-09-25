# LabFleet Ubuntu host configuration

Issue #7 prepares **disposable Ubuntu 24.04 nodes** for a future kubeadm cluster.
It does not create a cluster, install kubeadm/kubelet/Cilium, or configure the
coding-agent, Proxmox management, or persistent provisioner.

**Status:** implemented and offline-tested; live installation and two-run
idempotence are **unverified**. The existing PXE service is stopped and there is
no disposable target. Issue #7's explicit read-only provisioner boundary prevents
activating it. See [validation and limitations](../docs/ansible-host-configuration.md).

## Toolchain

Use an existing automation runner or an isolated Python virtual environment in a
build workspace. Do not install system packages on the coding-agent to run this.

```sh
python3 -m venv /private/build/labfleet-ansible
/private/build/labfleet-ansible/bin/pip install -r ansible/requirements-controller.txt
export PATH=/private/build/labfleet-ansible/bin:$PATH
cd ansible
```

The tested top-level tools are pinned to `ansible-core==2.20.3` and
`ansible-lint==26.3.0`; core 2.20 needs a supported Python on the controller
(the validation runner used Python 3.14). No external Ansible collections are
required. Transitive Python packages are not a hash-locked supply-chain bundle.
If `venv` is unavailable on a protected runner, use a build-local virtualenv
bootstrap or a different existing runner, not an OS package installation.
No Ansible/Python installation step was added to CI. This avoids silently adding
unreviewed CI dependencies; the Ansible checks below are explicit local checks.

## Inventory and authentication

Copy `inventory/example/hosts.yml` into ignored `inventory/local/hosts.yml`.
The example deliberately cannot run: its address/node values are placeholders
and `labfleet_disposable` is false. Supply real values only in private inputs:

- `labfleet_nodes`: the **only** group selected by the playbook.
- `bootstrap_control`: documentation/exclusion group, not a normal target.
- `ansible_host`: the target's IPv4 address, not the SSH jump host address.
- `ansible_user`: existing non-root provisioning-created administrator.
- `labfleet_vm_id`, `labfleet_proxmox_node`, `labfleet_expected_hostname`:
  selectors for independent live API verification, not ownership proof alone.
- `labfleet_disposable: true`: explicit opt-in, insufficient without API evidence.

Provide the existing Proxmox API credentials through `PROXMOX_VE_ENDPOINT` and
`PROXMOX_VE_API_TOKEN`, using the authorized credential mechanism. The token may
include the `PVEAPIToken=` prefix. Only read-only VM configuration requests are
made; credentials are not sent to guests, saved in inventory, or logged by the
credential-bearing tasks. TLS validation is enabled. If the lab uses a private
CA, trust that CA on the automation runner; `safety_validate_certs: false` is an
explicit private lab override, not the default. Do not use it for untrusted
endpoints.

Use a private SSH configuration/known_hosts file and `ansible_ssh_common_args`
for a pinned `ProxyJump` through the provisioner if necessary. The playbook
requires public-key SSH, strict host-key verification and a non-root login. Keep
private keys on the controller; do not copy keys to the provisioner, enable agent
forwarding, or disable host-key checks as a shortcut. Supply privilege-escalation
credentials through an external secret mechanism or `--ask-become-pass`.

**PXE node profile:** The default Issue #4 account has key access but no usable
password or sudo password. For unattended configuration, the provisioning renderer
now offers explicit `passwordless_sudo: true`, scoped to the disposable guest's
existing `fleet` user. Root-owned mode-0440 sudoers is validated with `visudo`.
This root-equivalent privilege is necessary for apt, systemd, kernel and system-file
management, not granted to the controller or provisioner. Ansible itself does not
grant administrative access. Pair this opt-in with the dual-NIC management uplink
documented in [provisioning](../docs/provisioning.md); no provisioner routing is
needed. Direct management SSH uses the same strict public-key/host-key policy.

## Safety preflight

Before any package/file/service mutation, the `safety` role:

1. Requires explicit disposable-node inventory and SSH (not local execution), a
   non-root user, a reserved VM ID, and a `labfleet-` non-provisioner hostname.
   It excludes bootstrap/control groups and known protected IDs.
2. Reads the selected QEMU VM's configuration from Proxmox. The name must match;
   **both `labfleet` and `disposable` tags** are required. Provisioner/bootstrap/
   control tags, deletion protection, or absent UUID/MAC evidence are rejected.
3. Reads guest facts over pinned SSH with the existing become path. Its DMI UUID
   and interface MAC must match the live API configuration, the SSH address must
   belong to the guest, and it must be an Ubuntu 24.04 guest with the expected
   hostname. A hostname or inventory label alone cannot authorize configuration.
4. Rejects existing Kubernetes control/worker markers before configuration.

Fact gathering is read-only but requires privilege to read DMI reliably. API
errors, missing credentials, missing DMI evidence and failed become all fail
closed. Check mode still runs the read-only identity verification. The preflight
has the `always` tag. Do not bypass it with `--skip-tags always`, custom role-only
plays, altered connection arguments or untrusted inventories: Ansible inputs and
CLI flags are privileged automation code, not an adversarial security sandbox.

## Roles and variables

| Role | Managed state / principal variables |
|---|---|
| `base` | Apt metadata cached for one hour, safe upgrades with lock waits/retries, base tools; `base_apt_upgrade`, `base_packages` |
| `ssh` | Key-only non-root SSH drop-in, validation before handler-driven reload, reconnect check; `ssh_config_path` |
| `time` | Installed/enabled systemd-timesyncd, optional source list, bounded synchronization verification; `time_ntp_servers`, `time_wait_for_sync` (true) |
| `kubernetes_prereqs` | Swap disabled, active fstab swap entries commented, modules and networking sysctls persisted and checked; `kubernetes_prereqs_kernel_modules`, `kubernetes_prereqs_sysctls` |
| `containerd` | Ubuntu containerd 1.7 or 2.2 package, version-aware CRI configuration, runc/systemd cgroups, handler-driven restart, CRI health check; `containerd_config_path` |

Base tools include CA certificates, curl, GnuPG, iproute2, OpenSSH, Python, sudo,
conntrack, socat, kmod, procps and util-linux. Existing account/key files are not
replaced. No unattended reboot is performed; a pending reboot requirement is reported and must be
resolved through the owned-node lifecycle before claiming a reboot-tested host.

Default kernel modules: `overlay`, `br_netfilter`. Persistent sysctl values:

```text
net.ipv4.ip_forward = 1
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
```

These forwarding settings apply **only to disposable nodes**, never to the PXE
provisioner. Modules are loaded only when absent; sysctl drift is repaired only
when necessary. Managed files use explicit root ownership/modes. Swap is disabled
for the future kubelet's default fail-on-swap architecture. Enabled custom
systemd swap units or remaining active swap cause a failure instead of a false
readiness claim; nonstandard zram/generator services require separate review.

The role detects the installed version: containerd 1.7 receives config version 2;
containerd 2.2 receives config version 3 with separate CRI runtime/images plugins
and the v3 pinned sandbox image location. Both select `io.containerd.runc.v2` with
`SystemdCgroup = true`. Other minor families fail closed pending validation.
Ubuntu 24.04's current 2.2 package is used rather than pinning an obsolete 1.7
package. On a pristine check-mode host with no binary, the template preview uses
version 3, without claiming an installed or verified runtime.
No Kubernetes version, package repository, CNI, registry authentication,
kubeconfig, cluster certificate, or control-plane configuration is introduced.

## Checks and operation

From `ansible/`, with the isolated tools on PATH:

```sh
ansible-inventory -i inventory/example/hosts.yml --list
ansible-playbook playbooks/configure-node.yml --syntax-check
ansible-lint
python -m unittest discover -s tests -v

# Run only once an independently attested disposable node and become path exist:
ansible-playbook -i inventory/local/hosts.yml playbooks/configure-node.yml --check --diff
ansible-playbook -i inventory/local/hosts.yml playbooks/configure-node.yml
ansible-playbook -i inventory/local/hosts.yml playbooks/configure-node.yml
```

Use the **same inventory, variables and command** for both live runs. In normal
steady state within the apt cache window, the second recap should have
`changed=0 failed=0`. Package/security updates newly published between runs,
expired metadata cache, actual configuration drift, or a stopped service can
legitimately cause changes; investigate and record them rather than suppressing
Ansible's change reporting. Command tasks are confined to kernel/runtime probes
and operations without an appropriate built-in module, and reads report no
changes. Service restarts are handlers, not unconditional tasks.

Check mode does not install missing packages or load missing modules. A pristine
node may therefore lack services/binaries needed by downstream checks; syntax
and guard-only tests are not a substitute for the full live run. Use full check
mode most meaningfully after an initial successful configuration.

Offline tests execute real assertion tasks using synthetic inventory/API/guest
facts, without contacting any guest or API. They test protected IDs/groups,
missing ownership, persistent/provisioner identity, wrong UUID/MAC/address/OS and
the full play's early rejection path. None runs a configuration task on a
bootstrap/control system.

## Troubleshooting and deferred work

- **PXE unavailable:** do not activate/reconfigure a read-only provisioner under
  Issue #7 authority. This is the present live-validation blocker.
- **Become fails:** preserve the key-based path; obtain the authorized target
  administrative path rather than weakening SSH or granting broad privileges.
- **Apt/NTP unreachable:** the isolated PXE network advertises no gateway/DNS.
  Targets need explicitly authorized package repositories and NTP connectivity
  before running these roles. SSH ProxyJump does not provide Internet routing.
  Do not enable forwarding/NAT on the protected provisioner as a workaround.
- **SSH reload validation fails:** do not restart blindly; inspect conflicting
  existing drop-ins. A fresh key-authenticated connection must still succeed.
- **Containerd wrong version/CRI unhealthy:** inspect installed package and
  service logs. Do not disable the CRI check or claim 2.x support.
- **Second run changes:** use Ansible task/handler output to locate cache expiry,
  package updates or drift; do not blanket-apply `changed_when: false` to writes.

Kubeadm init/join, Kubernetes binaries/version selection, kubelet configuration,
Cilium, cluster networking and workloads are explicitly deferred to the next
roadmap issue. This issue does not start that work.
