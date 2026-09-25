# Issue #7: automated host configuration and validation

## Scope and current result

The implementation prepares owned, disposable Ubuntu 24.04 guests with Ansible
for later kubeadm use. It includes base packages/updates, key-preserving SSH
configuration, systemd-timesyncd, kernel modules/sysctls, swap handling and
containerd 1.7 with CRI/systemd cgroups. It does **not** bootstrap Kubernetes.
The [Ansible guide](../ansible/README.md) documents inventory, roles, variables,
credentials, safety checks, commands and troubleshooting.

**Live acceptance is incomplete.** Offline syntax, lint, inventory, role-contract
and safety tests are useful evidence, but they do not establish successful
installation or idempotence on a real node. The Issue #7 PR must remain draft.

## Read-only environment discovery and genuine blocker

At Issue #7 start, Issue #4 was merged into `main` at
`fabc6a24c93dbadb1c1df469f686fb447ed4c58d`. The working tree was clean, `main` was
fast-forwarded, and branch `feat/7-ansible-host-configuration` was created.

Read-only Proxmox inventory contained only the protected coding-agent guest and
the persistent `labfleet-provisioner` (VM `930040`, tags `labfleet;provisioner`).
There was **no disposable target**. Read-only service inspection of the
provisioner returned:

```text
labfleet-provisioning.service: inactive
unit enablement: disabled
provisioning DHCP sockets: 0
```

The Issue #7 request explicitly forbids modifying the persistent provisioner.
Starting its PXE service would violate that restriction, even without editing
files. Accordingly, no service was started, no routing/firewall/management
configuration changed, and no substitute provisioning infrastructure was
invented. Creating a blank VM with no available boot service would not validate
Ansible and would leave unnecessary test infrastructure, so none was created.

There are two additional prerequisites to resolve before a future live run:

- Issue #4's installed `fleet` user supports SSH keys but has no usable password
  or passwordless sudo. The complete play needs an independently authorized
  target-only become path; it must not silently create broad administrative
  privileges or weaken SSH to obtain one.
- The isolated PXE network advertises neither gateway nor DNS. Apt repositories
  and NTP sources must be reachable through an explicitly authorized target
  network arrangement. ProxyJump is an SSH transport, not package/NTP routing.
  The read-only provisioner must not be turned into a router as a workaround.

No protected host was run through Ansible, even in check mode. The only
provisioner access was read-only environment inspection. Build tools were
installed into a private temporary virtual environment, not system Python or OS
packages; no coding-agent machine/network configuration was changed.

## Acceptance matrix

FAIL below means **blocked/unverified**, not that a live playbook failed.

| GitHub Issue #7 acceptance criterion | Result and evidence |
|---|---|
| Inventory supports LabFleet nodes | PASS — explicit `labfleet_nodes` group and private-input model |
| Coding-agent excluded by default | PASS — play targets only `labfleet_nodes`; bootstrap-only fixture matches no hosts |
| Base configuration completes successfully | FAIL — no eligible live node; not exercised |
| Containerd installs successfully | FAIL — package/configuration/health tasks exist, no live installation |
| Kubernetes prerequisites configured | FAIL — module/sysctl/swap tasks exist, not applied to a live node |
| Running twice produces no unnecessary changes | FAIL — no first or second live run; convergence is not claimed |
| No secrets in committed inventories | PASS — placeholders, synthetic fixture identities and documentation addresses only |
| Example inventory provided | PASS — intentionally non-deployable example with explicit opt-in disabled |
| Accidental bootstrap targeting prevented/guarded | PASS — group/ID/tag/protection guards, live API ownership and guest UUID/MAC checks; offline rejection tests |

## Live evidence ledger

| Requested evidence | Observed result |
|---|---|
| Disposable target identity / Ubuntu version | None; no target created or configured |
| First Ansible run: ok / changed / failed | N/A / N/A / N/A — not run |
| Second run: ok / changed / failed | N/A / N/A / N/A — not run |
| Second-run changes remaining | Unknown; not measured |
| Containerd version / running state | Unverified; configuration supports Ubuntu containerd 1.7 |
| `overlay`, `br_netfilter` | Intended persistent modules; live load state unverified |
| IPv4 forwarding and bridge netfilter sysctls | Intended values are all `1`; live values unverified |
| Swap | Intended inactive and fstab-disabled; live result unverified |
| Time synchronization | systemd-timesyncd installed/enabled and synchronization required by default; live result unverified |
| SSH after configuration | Unverified; no live configuration occurred |
| Bootstrap/control systems targeted | None; synthetic assertion tests only |
| Issue #7 disposable resources remaining | None created |

Do not substitute the offline tests' `changed=0` assertion recaps for a live
idempotence result. Issue #4's successful PXE installations also do not establish
Issue #7 host-configuration success.

## Offline validation and fixes

The isolated toolchain uses Ansible Core 2.20.3 and Ansible Lint 26.3.0. No new
Ansible dependency installation was added to CI. Run from `ansible/` so the
repository configuration and role paths resolve correctly:

```sh
ansible-inventory --list
ansible-playbook playbooks/configure-node.yml --syntax-check
ansible-lint
python -m unittest discover -s tests -v
```

The tests execute the actual controller-side Ansible assertions against synthetic
fixtures. They cover positive disposable ownership, protected IDs and groups,
missing tags/UUID/MAC, deletion protection, wrong guest UUID/MAC/address/OS,
missing opt-in and local/root connections. The full play rejects a protected
fixture before any API request or configuration role; a bootstrap-only inventory
matches no hosts. Skipping the `always` tag is also rejected by a second pre-task.
Tests do not SSH to a control host or send a real API request.

Role-contract tests check the actual containerd version guard using positive
1.7 and negative 1.6/2.x fixtures, parse its TOML for CRI/systemd cgroups, and
reject conflicting effective SSH policy. Review caught a Jinja/regex escaping
error that rejected valid containerd versions; the expression now avoids those
backslash escapes and is regression-tested. SSH syntax validation is followed by
an effective global `sshd -T` policy check before reload, then a fresh key-only
connection. Custom `Match` blocks require additional site-specific audit.

Other corrections before publication included package/config-directory creation
for timesyncd, proper namespaced fact access, handler-driven sysctl application,
runtime drift verification, custom enabled swap-unit rejection, and placing
kernel prerequisites before containerd. None of these offline fixes is presented
as a live-tested result.

The ordinary repository Go/Make and both OpenTofu root checks must also pass
before publication. These do not apply infrastructure and do not start work on
another issue. Credentials, real addresses, local inventory, keys, state, caches
and raw environment discovery stay outside Git.

Observed local results: inventory export, playbook syntax, production-profile
Ansible Lint (zero failures/warnings), and **10 offline test methods** with their
positive/negative fixture cases passed. `go build ./...`, `go test ./...`,
`go test -race ./...`, all four Make targets, and both OpenTofu roots' formatting,
readonly-lockfile initialization, validation and **18 mocked tests** passed.
`git diff --check` passed. None of these commands applied infrastructure.

## Completing live acceptance under a compatible scope

Once the existing provisioning service is available and target-only
administrative/package/NTP access has been established under appropriate
authority:

1. Use Issue #4's guarded OpenTofu/PXE path for exactly one compatible blank
   disposable guest. Require live `labfleet;disposable` tags, no local media and
   the expected serial/MAC; do not manually build the node in a GUI.
2. Pin its SSH host key, confirm the provisioning-created administrative path,
   and capture OS, package/service, module/sysctl, swap and time baselines.
3. Supply private inventory/environment credentials, run syntax/inventory/guard
   checks, then the complete playbook. Preserve its actual first-run recap.
4. Verify SSH from the coding-agent through the existing pinned jump path,
   containerd version/CRI health, module and sysctl persistence, inactive swap,
   NTP synchronization, and any reported reboot requirement.
5. Run the **exact same** command/inputs again. Require `failed=0` and steady-state
   `changed=0`; investigate task-level changes rather than hiding them. If fixes
   are needed, repeat the pair and retain honest results.
6. Run check mode against the configured target. Demonstrate control exclusion
   with inventory/fixture tests, not mutations against protected hosts.
7. Guard and destroy only that disposable VM, verify its disk removal and compare
   protected baselines. Retain the persistent provisioner/network unchanged.
8. Replace this blocked ledger only with observed evidence, rerun tests and CI,
   and mark the existing Issue #7 PR ready. Do not merge or start Kubernetes
   bootstrap as part of this issue.
