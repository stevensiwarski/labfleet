# Issue 8: Disposable six-node Kubernetes cluster operator guide

## Purpose, audience, and status

**Audience:** LabFleet operators authorized to manage the dedicated disposable Issue 8 VMs on the nested Proxmox lab. This is an execution and recovery guide, not authorization to alter host networking or bootstrap/control systems.

**Thesis:** Reproducibly create six owned Ubuntu nodes, attest identity before configuration, bootstrap a kubeadm cluster in an explicit safe order, validate it, and export an operator kubeconfig without exposing cluster credentials.

**Non-goals:** Production/high-availability guarantees; modifying the management hypervisor, coding-agent, bridges or host routing; using the provisioner beyond its intended provisioning role; managing arbitrary VMs; automatically upgrading Kubernetes; installing system packages on the controller; or representing planned validation as completed live evidence.

**Status:** Issue #8 is live validated, including a complete first-cluster destruction and fresh six-node recreation. The recreated cluster is retained healthy; its identical bootstrap rerun reports zero changes and failures on every node. The evidence below comes from Issue #8 itself, not substituted Issue #4/#7 results.

## Decision-relevant design

The cluster is six explicitly reserved LabFleet disposable VMs, not existing infrastructure adopted into state:

| Role | Name | VM ID | Shape |
|---|---|---:|---|
| Control plane 1 | `labfleet-cp-01` | 930081 | 4 vCPU, 16 GiB RAM, 32 GiB disk |
| Control plane 2 | `labfleet-cp-02` | 930082 | 4 vCPU, 16 GiB RAM, 32 GiB disk |
| Control plane 3 | `labfleet-cp-03` | 930083 | 4 vCPU, 16 GiB RAM, 32 GiB disk |
| Worker 1 | `labfleet-worker-01` | 930084 | 4 vCPU, 16 GiB RAM, 32 GiB disk |
| Worker 2 | `labfleet-worker-02` | 930085 | 4 vCPU, 16 GiB RAM, 32 GiB disk |
| Worker 3 | `labfleet-worker-03` | 930086 | 4 vCPU, 16 GiB RAM, 32 GiB disk |

The IDs and names are fixed in `infra/opentofu/cluster/main.tf`; the OpenTofu root sets exact ownership/role tags (`labfleet`, `issue8`, `disposable`, and role), QEMU guest agent, a blank disk with a LabFleet VM-specific serial, and PXE-first boot. VMs start **stopped** (`started=false`); they do not auto-start after host reboot. The 16 GiB RAM choice reflects Issue 4/7 observations: 8 GiB failed the Ubuntu installer while 16 GiB completed. It is an empirical provisioning floor, not a claim of production sizing.

```text
                    NIC0: existing management bridge
                    mgmt0 / SSH / Kubernetes node traffic
                                |
      +-------------------------+-------------------------+
      |       cp-01  cp-02  cp-03  worker-01..03         |
      +-------------------------+-------------------------+
                    NIC1: existing isolated PXE bridge
                    fixed MACs / PXE only / no gateway or DNS
                                |
                    existing LabFleet provisioner
```

NIC0 is the existing management bridge with its fixed management MAC; NIC1 is the existing isolated PXE bridge with its fixed provisioning MAC and is first in boot order. The renderer provisions Ubuntu 24.04.5, configured so the provisioning NIC does not replace management default route/DNS. Kubernetes traffic, including the selected API endpoint and Cilium VXLAN overlay (UDP 8472), uses `mgmt0`. The PXE network advertises no gateway/DNS. **Do not modify either bridge, add a gateway/NAT, or configure forwarding/router services on the provisioner** to make downloads work. Establish authorized package/NTP reachability over management before proceeding.

The API endpoint is the management address of `labfleet-cp-01` on port 6443. Three control planes provide three etcd members but this endpoint is deliberately **not a virtual IP or HA load balancer**: loss of cp-01 can make API access fail even if other control planes remain. Do not describe this topology as highly available.

The repo pins Kubernetes `1.36.4` (`kubernetes_minor: 1.36`, apt version `1.36.4-1.1`) from the official `pkgs.k8s.io/core:/stable:/v1.36/deb/` repository; kubeadm init/join use that release. The supported 1.36 minor is explicitly in Cilium 1.20's tested compatibility set and was selected intentionally rather than using `latest`. The chosen documented stable patch was 1.36.4; upstream package metadata showing a newer 1.36.5 ahead of the selected documented release is not a reason to silently drift pins. Update the repo's release selection deliberately before an upgrade, including repository minor, all three package versions, holds and kubeadm's supported upgrade order. Cilium is pinned to chart 1.20.2 with the repository's SHA-256; Helm 3.17.3 is pinned with its binary archive SHA-256. Values select Kubernetes IPAM, retain kube-proxy, VXLAN tunneling, IPv4 only, and `mgmt0` as device. Existing chart-version or values drift fails rather than silently upgrading or pretending convergence. Do not switch kube-proxy replacement or routing mode by hand.

## Preconditions and private working inputs

1. Use an authorized automation runner, with OpenTofu, Go, Python/Ansible and the existing network access already available. Follow [Ansible host configuration](ansible-host-configuration.md) for the isolated runner/controller toolchain; do not install controller packages on the coding-agent VM.
2. Confirm the existing PXE service is healthy and configured only on the isolated provisioning interface. Review the existing Issue 4 [provisioning procedure](provisioning.md). The Issue 8 profile requires six target definitions and a single fleet-capable provisioning service configuration; use the current Go `provisioning` renderer and its `targets` array, not six ad hoc service deployments. For each target, map OpenTofu output's `vm_id`, `name`, provisioning `mac`, `management_mac`, `role`, and tags to the renderer's `target_ip`, `target_mac`, `management_mac`, `target_hostname`, and `disk_serial` fields. Select distinct, authorized addresses on the existing networks and derive each disk serial from the VM-specific serial contract. Keep the generated mapping/configuration and private key material private. Do not copy keys to the provisioner.
3. Confirm the six reserved IDs are all unused or already belong to this exact LabFleet state, and confirm adequate nested host capacity. Never infer ownership from a high ID, name, tag proposal, or generated inventory alone. The OpenTofu example tfvars is absent for this dedicated root; prepare an ignored/private `local.tfvars` with the existing Proxmox node, datastore, existing PXE bridge, distinct existing management bridge, and `ownership_confirmed=true` only after this check. Never create or reconfigure a bridge as part of this procedure.
4. Supply the existing Proxmox API credentials through the authorized environment mechanism (`PROXMOX_VE_ENDPOINT`, `PROXMOX_VE_API_TOKEN`). Use a restricted token and TLS verification. Do not put credentials in command arguments, tfvars, inventory, logs, or this document.
5. Have the existing PXE renderer's authorized Ubuntu artifact cache available and verify its manifest/checksums. PXE includes provisioning key access and the scoped disposable-node `fleet` account profile described by the Issue 7 guide. Root-equivalent passwordless sudo, if enabled by that explicit disposable guest profile, is only on the guests; it is not a privilege grant on the controller or provisioner.
6. Provide the existing private SSH key path and an absolute private discovery directory (mode 0700). Ensure the operator can reach discovered management addresses directly or by a strict, private SSH `ProxyJump` configuration. Keep strict host-key checking enabled.

## Phase 1 — Plan and create only the six stopped VMs

OpenTofu state belongs to the `infra/opentofu/cluster` root and must remain private. From the repo root:

```sh
make build
cd infra/opentofu/cluster
umask 077
tofu init -input=false -lockfile=readonly
tofu fmt -check
tofu validate
tofu test
tofu plan -input=false -var-file=local.tfvars -out=create.tfplan
tofu show create.tfplan
tofu show -json create.tfplan > create.tfplan.json
../../../bin/fleetctl tofu-check -plan create.tfplan.json
tofu apply -input=false create.tfplan
```

The checker is required on a fresh complete saved plan before **every** apply and destroy. Do not use `tofu apply` with a newly generated plan, target a subset, disable refresh, import, or bypass this check. Review all six IDs, VM identity, tags, disks, both existing bridge references, and stopped state. Apply only the exact checked plan. If existing state is missing or ownership is ambiguous, stop; do not import or delete.

The VMs are initially stopped to prevent them PXE-booting before the multi-target service is configured. Once all six provisioning targets are safely rendered, installed, and the existing provisioner service validated, set `started=true` in private tfvars, make and inspect a new plan, run `fleetctl tofu-check`, and apply that exact plan. This update intentionally starts the six VMs into PXE. Observe provisioning through the existing service; do not expand its network scope. PXE/Ubuntu installation can exceed the normal short API wait: allow up to 15 minutes for discovery readiness. A previous 120-second QGA wait timed out with guests still running; a timeout is not permission to start duplicate targets or destroy them. Inspect live state and reconcile deliberately.

## Phase 2 — Attest and discover (do not hand-author IP inventory)

The OpenTofu `fleet` output is the authoritative source for all six MAC/name/role/ID mappings. Capture it privately and use a small local JSON transformation to fill the existing provisioner's `targets` array; do not manually transcribe six identities or paste actual private addresses into documentation. `ansible/playbooks/discover-cluster.yml` takes a matching six-entry `cluster_fleet` variable with `name`, `vm_id`, provisioning `mac`, management `management_mac`, and `role`. Supply the provisioner node and expected hostname fields required by its child task; use the exact six names, IDs, MACs, and role values from this root's output.

For example, use the existing private service configuration as
`provisioning-base.json`, and a private six-element JSON list of reserved PXE
addresses as `pxe-addresses.json`. The list follows the fleet output order; no
individual guest configuration commands are needed:

```sh
export PRIVATE_DIR=/private/path/issue8
umask 077
tofu -chdir=infra/opentofu/cluster output -json fleet > "$PRIVATE_DIR/fleet.json"
python3 - <<'PY'
import json, os
from pathlib import Path
p = Path(os.environ['PRIVATE_DIR'])
fleet = json.loads((p / 'fleet.json').read_text())
addresses = json.loads((p / 'pxe-addresses.json').read_text())
assert len(fleet) == len(addresses) == len(set(addresses)) == 6
config = json.loads((p / 'provisioning-base.json').read_text())
for field in ('target_ip', 'target_mac', 'management_mac', 'target_hostname', 'disk_serial'):
    config.pop(field, None)
config['passwordless_sudo'] = True
config['targets'] = [dict(target_ip=address, target_mac=node['mac'],
    management_mac=node['management_mac'], target_hostname=node['name'],
    disk_serial='SQEMU_QEMU_HARDDISK_labfleet-k8s-' + str(node['vm_id']))
    for node, address in zip(fleet, addresses)]
(p / 'provisioning.json').write_text(json.dumps(config))
PY
```

Build the repository's `provisionctl`, install it and this validated private
configuration on the owned provisioner using the existing provisioning service
procedure, then run `provisionctl check` before starting the service. Do not
change its interface binding or artifact verification. For discovery, the private
extra-vars JSON contains `cluster_fleet` (the fleet list), `labfleet_proxmox_node`,
`cluster_private_dir`, and `cluster_ssh_private_key_file`; optionally set the
explicit private-lab TLS override. The outputs are `inventory.json` and
`known_hosts` under that private directory.

From `ansible/`, run discovery with the private values passed through an ignored vars file (e.g. `-e @/private/path/issue8-discovery.json`) and environment-injected API credentials:

```sh
ansible-playbook -i inventory/example/hosts.yml playbooks/discover-cluster.yml \
  -e @/private/path/issue8-discovery.json
```

The documentation example inventory is not a runnable cluster inventory. Discovery verifies all six fixed IDs, unique MACs, three roles each, API ownership/role tags, exact VM names, deletion protection and management NIC mapping. QGA supplies `mgmt0` addresses and fresh SSH public host keys; strict host-key checks are not bypassed. Bootstrap then independently verifies guest UUID/MAC/OS/hostname against API evidence over that pinned SSH connection. API TLS validation defaults true; the private lab used an explicit override for its private endpoint, not a change to the default. Discovery writes `known_hosts` and JSON inventory under `cluster_private_dir` with modes 0600, in a 0700 directory. Its only guest command reads the public SSH host key. Inspect the generated private inventory for exactly 3+3 roles and unique management addresses; never place it in git or print credential-bearing output.

## Phase 3 — Prepare and bootstrap in order

Run from `ansible/`, using the generated inventory and its pinned known_hosts. Every mutating play runs an independent Proxmox and guest identity/role/interface safety preflight. The complete six-host play is intentional: do not use `--limit` to avoid cluster-wide checks. The control-plane endpoint comes from cp-01's discovered `mgmt0` address.

```sh
ansible-playbook -i /private/path/issue8/inventory.json \
  playbooks/bootstrap-cluster.yml --syntax-check
ansible-playbook -i /private/path/issue8/inventory.json \
  playbooks/bootstrap-cluster.yml
```

Play ordering is safety-significant:

1. Attest **all six** first, then prepare only nodes without `/etc/kubernetes/kubelet.conf` (base, SSH, time, Kubernetes prerequisites, containerd). Install pinned Kubernetes packages. A second pass is designed to be idempotent; investigate reported changes rather than hiding them.
2. Initialize cp-01, then join cp-02 and cp-03, serially in inventory order. This is the configured first control-plane bootstrap, then additional control-plane join flow; do not run parallel control-plane init.
3. Join the three workers only after all control planes.
4. From cp-01, label workers, install the pinned Cilium release, and run cluster validation. kube-proxy remains in place.

The cluster bootstrap is not an automatic upgrade mechanism. Package versions are pinned/held; do not unhold/upgrade packages or rerun init as a repair shortcut. If a run fails, retain evidence, inspect the exact node/runtime state, and resume only through the guarded playbook's supported runtime-aware path. Join credentials are short-lived and only used when unjoined peers remain. Never print, store in notes, or manually redistribute join tokens/certificates. Do not reset a node or wipe `/etc/kubernetes` to force a rerun without independently proving ownership and a reviewed recovery plan.

The bootstrap play's `cluster_validation_run_network` defaults to false. Its default validation checks API readiness, nodes and system pods without creating test workloads. For a separate, independent cross-worker connectivity check, the repo provides `validate-cluster.yml`; this play explicitly creates temporary validation workloads (it sets `cluster_validation_run_network: true`), so review its effect and record/confirm cleanup:

```sh
ansible-playbook -i /private/path/issue8/inventory.json \
  playbooks/validate-cluster.yml
```

Bootstrap can also opt in to its role's ephemeral test with `-e cluster_validation_run_network=true`. Either path uses temporary resources in `labfleet-validation`; record which was used and verify cleanup.

The test uses pinned image tags (`registry.k8s.io/e2e-test-images/agnhost:2.53`
and `busybox:1.37.0`) on two distinct workers. It checks direct PodIP and
ClusterIP HTTP, service-name access, `kubernetes.default.svc.cluster.local`,
external DNS, and an Ubuntu repository HTTP fetch. The namespace must not
already exist; creation records its UID and cleanup verifies that identity.
Cilium agent pods are selected by `k8s-app=cilium`, not a name prefix that also
matches its operator/Envoy pods. The health probe must report 6/6 reachable.

## Phase 4 — Verify and export operator access

Run the repository's independent cluster validation play; do not treat a successful bootstrap Ansible recap alone as proof of healthy Kubernetes. At minimum record time-stamped, redacted evidence of: exact six VM IDs and LabFleet tags; successful PXE/OS configuration; all six nodes `Ready` with three control-plane and three worker roles; Kubernetes client/server version; Cilium chart/image readiness; system pod readiness; and whether optional pod-to-pod connectivity was exercised. Record failures and limitations, not just successes. Never copy API tokens, join tokens, private keys, certificate key, or client certificate/key contents into the ledger.

Export kubeconfig only after the independent safety preflight succeeds:

```sh
ansible-playbook -i /private/path/issue8/inventory.json \
  playbooks/export-kubeconfig.yml \
  -e cluster_kubeconfig_path=/home/OPERATOR/.config/opencode/runtime/kubeconfigs/issue8.kubeconfig
```

Use the actual operator home path. The play reads `/etc/kubernetes/admin.conf` from the first control plane only after attesting the complete cluster and writes the file on the controller with parent directory mode 0700 and file mode 0600. `no_log`/no-diff protections are intentional. Set `KUBECONFIG` explicitly for a private, already available `kubectl` binary; verify its identity/version and node status without echoing credential content. Do not install `kubectl` on the protected coding-agent system as part of this guide. Since this is not HA, the kubeconfig's cp-01 endpoint remains a single endpoint.

## Teardown and protected-resource boundary

Issue 8 VMs are disposable; a successfully bootstrapped cluster may be retained for operator use. Do not run destruction just because the guide reaches its end. If teardown is explicitly intended, first verify all six IDs are still precisely the LabFleet-owned resources in this root and state, confirm no valuable workload data is present, and confirm destruction will not affect management/provisioner systems. Build and inspect a fresh destroy plan, run the same `fleetctl tofu-check`, and apply only that exact saved plan. Destroy stops/deletes only the six managed guests and attached disks according to this root; do not broaden cleanup to networks, the provisioner, unreferenced disks, or unrelated state.

For the required reproducibility test, first discard the first cluster's local kubeconfig and generated discovery inventory/host keys, then destroy the six owned VMs and verify their disks are absent. Recreate stopped blank guests, revalidate the six-target PXE profile, start them, and repeat discovery/bootstrap/validation with newly generated cluster credentials. Do not reuse first-cluster certificates or join material. Return PXE to its prior inactive/disabled state when provisioning is finished; do not disrupt another authorized user's provisioning run. Keep the reusable operator SSH key private, but remove only this task's generated credential files. A kubeconfig deletion does not revoke a still-valid cluster credential; destroying the first cluster and its disks makes its credentials unusable against the freshly generated second cluster.

## Operational limitations

- The cp-01 API endpoint itself is not HA; no failover VIP or external load balancer is introduced.
- The initial cluster uses management DHCP leases. Stable reservations are advisable for longer-lived clusters; arbitrary management-address changes are not automatically repaired.
- Package and Cilium version changes require an explicit upgrade procedure, not a normal rerun.
- No automatic node reboot occurs. Host-preparation roles report a pending reboot; bootstrap does not claim reboot/failover testing.
- Existing cluster members skip pre-cluster host preparation to avoid changing a live cluster's base configuration implicitly. Node readiness/runtime health is still checked.
- Ansible validation runs locally with pinned tooling; GitHub Actions covers Go and all three OpenTofu roots, not live Kubernetes or Ansible execution.
- Temporary network-validation workloads intentionally report create/delete changes. The ordinary bootstrap rerun does not create those workloads and must converge without changes.

## Live evidence ledger

The first six-node cluster completed independent live validation: exactly three
control planes and three workers were Ready on v1.36.4/containerd 2.2.1, with
management InternalIPs. Cilium health reported **6/6 reachable**, all three etcd
members passed membership/endpoint checks, and CoreDNS was healthy. Temporary
pods on different workers passed PodIP, ClusterIP, service-name, cluster DNS,
external DNS and external HTTP repository checks; the namespace was removed.
The coding-agent successfully accessed the API with its private exported config.

The final first-cluster bootstrap rerun used unchanged inputs and reported:

| Node | ok | changed | unreachable | failed | skipped | rescued | ignored |
|---|---:|---:|---:|---:|---:|---:|---:|
| cp-01 | 56 | 0 | 0 | 0 | 66 | 0 | 0 |
| cp-02 | 28 | 0 | 0 | 0 | 55 | 0 | 0 |
| cp-03 | 28 | 0 | 0 | 0 | 55 | 0 | 0 |
| worker-01 | 27 | 0 | 0 | 0 | 46 | 0 | 0 |
| worker-02 | 27 | 0 | 0 | 0 | 46 | 0 | 0 |
| worker-03 | 27 | 0 | 0 | 0 | 46 | 0 | 0 |

Earlier development failures were retained, not presented as successful first
runs: the original module-address plan was rejected by the existing ownership
checker before apply; worker joining stopped on a renamed-variable mismatch;
validation stopped on a legacy injected-fact name and then a Cilium pod selector
that included Envoy. The root resource address, worker template, fact access and
label-based selector were corrected and regression-tested. A namespace UID guard
and partial-package-version guard were also strengthened during review.

The required second lifecycle also completed. The old runtime kubeconfig and
generated inventory/known-hosts files were discarded before guarded teardown.
The plan deleted exactly six owned VMs; API checks confirmed all six absent.
Disk removal was reconciled after recreation rather than sampled during the empty
interval: each old volume name now refers to a newly created volume with a
creation time **after** the first destroy completed, and storage inventory shows
exactly one 32 GiB disk per VM with no matching orphan volumes. New nodes had no
kubelet configuration before bootstrap, and all six were PXE-installed again.

Fresh second-cluster bootstrap recaps:

| Node | ok | changed | unreachable | failed | skipped | rescued | ignored |
|---|---:|---:|---:|---:|---:|---:|---:|
| cp-01 | 110 | 35 | 0 | 0 | 16 | 0 | 0 |
| cp-02 | 73 | 24 | 0 | 0 | 14 | 0 | 0 |
| cp-03 | 73 | 24 | 0 | 0 | 14 | 0 | 0 |
| worker-01 | 73 | 25 | 0 | 0 | 4 | 0 | 0 |
| worker-02 | 73 | 25 | 0 | 0 | 4 | 0 | 0 |
| worker-03 | 73 | 25 | 0 | 0 | 4 | 0 | 0 |

The identical second-cluster rerun produced the **same zero-change recaps as the
first-cluster rerun table above**. Independent network validation passed on both
clusters: cp-01 `ok=36 changed=5 unreachable=0 failed=0 skipped=1 rescued=0 ignored=0`;
each other node `ok=12 changed=0 unreachable=0 failed=0 skipped=1 rescued=0 ignored=0`.
Those five intentional changes create the validation directory/manifest and
temporary namespace/workloads, then remove the namespace; they are not bootstrap
idempotence changes.

Second-cluster CA and admin-client certificate fingerprints and the `kube-system`
UID all differ from the first cluster. Only private fingerprints were retained
for comparison; no first-cluster kubeconfig, certificates or join credentials were
used to bootstrap the replacement.

Observed final results (2026-09-26 UTC; raw evidence remains private):

| Check | Result / evidence |
|---|---|
| Ownership/ID reservation and protected-system baseline | PASS — exact IDs/tags; protected VM/node/network configurations unchanged |
| Checked stopped-VM creation and live shapes | PASS — both six-create plans and first six-delete plan passed the existing guard |
| Multi-target PXE and Ubuntu 24.04.5 | PASS — all six installed twice; second run used blank disks |
| API/QGA/guest attestation and pinned SSH | PASS — six private entries with management IPs and fresh host keys |
| Fresh bootstrap and identical rerun | PASS — table above; zero unnecessary changes on every node |
| Three control-plane joins/membership and three workers | PASS — init cp-01, join cp-02/cp-03, then workers; three healthy etcd members/endpoints |
| Kubernetes / readiness / system pods | PASS — v1.36.4, exactly 6/6 Ready; final independent check found 33 Running pods with every container Ready |
| Cilium / CoreDNS / networking | PASS — Cilium 1.20.2, 6/6 health reachable, operators/CoreDNS Ready; cross-worker PodIP, Service, cluster/external DNS and external HTTP passed |
| Containerd | PASS — 2.2.1 active on all six nodes |
| API access and kubeconfig | PASS — coding-agent reached API; new admin config mode 0600 in runtime-scoped private path |
| Credential/state independence | PASS — new CA, admin-client certificate and cluster UID; no first-cluster credentials reused |
| Final retained infrastructure | PASS — six running owned VMs plus unchanged coding-agent and persistent provisioner; no validation namespace remains |
| PXE final state | PASS — original config/binary restored; inactive/disabled; no DHCP/TFTP listeners; forwarding remains 0 |

All nine GitHub Issue #8 acceptance criteria PASS: three Ready control planes,
three Ready workers, coding-agent excluded, healthy Cilium, healthy CoreDNS,
API reachable from the coding-agent, no manual node-by-node configuration,
full reproducible destruction/recreation, and no committed credential-bearing
kubeconfig. The provisioner is also excluded from Kubernetes.

## Local validation and publication boundary

Inventory/syntax checks, production-profile Ansible lint and **35 offline test
methods** passed. Tests include actual assertion/decision tasks, strict kubeadm
template rendering, ownership/role separation, partial package-version drift,
Cilium agent selection, and private/ignored kubeconfig handling. Go build, test,
race, all four Make targets, all three OpenTofu roots' formatting, readonly-lockfile
initialization, validation and **25 mocked tests** passed. Whitespace, candidate
credential/private-address/private-key/kubeconfig scans passed. Independent source
review found no remaining high-confidence blocker.

The final cluster is intentionally retained for later issues; no later issue
work is included here. PR readiness additionally requires the published head's
GitHub Actions to pass. This task does not merge the PR.

## Sources and implementation references

Release selection and artifact verification used the official
[Kubernetes releases](https://kubernetes.io/releases/),
[kubeadm package guidance](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/install-kubeadm/),
[v1.36 package repository](https://pkgs.k8s.io/core:/stable:/v1.36/deb/),
[Cilium 1.20 compatibility requirements](https://docs.cilium.io/en/v1.20/network/kubernetes/requirements/),
[Cilium chart index](https://helm.cilium.io/index.yaml), and
[Helm archive checksum](https://get.helm.sh/helm-v3.17.3-linux-amd64.tar.gz.sha256sum).
The controller's private kubectl 1.36.4 binary was checked against its official
`dl.k8s.io` adjacent SHA-256 file before use; no controller system package was installed.

This guide's implementation facts are sourced from the current repository: `infra/opentofu/cluster/{main.tf,variables.tf,outputs.tf,tests/cluster.tftest.hcl}`, `ansible/{vars/kubernetes.yml,playbooks/bootstrap-cluster.yml,playbooks/discover-cluster.yml,playbooks/validate-cluster.yml,playbooks/export-kubeconfig.yml}`, the `kubernetes_safety`, `cluster_discovery`, `kubeadm_control_plane`, `kubeadm_worker`, `kubernetes_packages`, `cilium`, and `cluster_validation` roles/defaults, and `provisioning/internal/pxe/config.go`. Operational safeguards and Issue 4/7 observations are in [provisioning](provisioning.md), [OpenTofu](../infra/opentofu/README.md), and [Ansible host configuration](ansible-host-configuration.md). For external release-specific compatibility evidence, consult the upstream Kubernetes stable package repository and release documentation, the Cilium compatibility matrix/chart documentation, and the official Helm release checksum page before changing pins; this guide does not claim those external references were freshly revalidated for another release.

**Redactions:** No real addresses, credentials, private keys, tokens, kubeconfig, certificate data, or live node outputs are included. The private inventory and runtime kubeconfig are expressly excluded from publication.

**Remaining limitations:** See the operational limitations above. No long-duration
soak, automatic upgrade, automatic reboot, or API-endpoint failover was claimed.
There is no remaining live acceptance blocker.
