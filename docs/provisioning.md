# Isolated Ubuntu PXE provisioning

## Status and live-test blocker

Issue #4 adds provisioning configuration and an independently testable service.
**End-to-end installation is not yet validated.** Read-only lab discovery found
only the coding-agent VM and the management-addressed bridge. There is no
positively identified dedicated provisioning network or LabFleet-owned service
host. No DHCP server was started and no test VM was created for this issue.

Do not satisfy these prerequisites by changing the coding-agent VM, the nested
Proxmox management instance, or the management bridge. A human must provide or
explicitly identify the isolated LabFleet provisioning segment and an owned
service host. This is an infrastructure-access/safety blocker, not evidence that
an unattended installation or DHCP-isolation test passed.

## Architecture and request flow

```text
LabFleet-owned service host                One LabFleet-owned blank guest
  management NIC (SSH only)                 allowlisted fixed MAC
  dedicated provisioning NIC <------------ dedicated provisioning network
    dnsmasq: DHCP + TFTP                       |
    provisioning HTTP service                 PXE firmware
       |                                      |
       +--- undionly.kpxe -------------------- iPXE
       +--- boot.ipxe ------------------------ local-disk attempt
       +--- vmlinuz / initrd / ubuntu.iso ---- installer if disk is blank
       +--- seed/user-data + meta-data ------- Ubuntu autoinstall
                                              |
                                         install to identified disk
                                              |
                                         reboot → local disk → SSH
```

The dnsmasq process provides a static lease to exactly one configured target MAC
and serves a BIOS iPXE image using TFTP. An iPXE user-class match breaks the chain
loop: firmware receives `undionly.kpxe`; iPXE receives an HTTP boot script. HTTP
serves only named boot artifacts and that target's NoCloud seed. There is no DNS
service, general DHCP pool, management-network DHCP, or automatic network setup.

The iPXE script first attempts `sanboot --no-describe --drive 0x80`. A genuinely
blank disk falls through to the installer. Once Ubuntu has installed a bootable
disk, a subsequent network-first firmware boot should hand off to that disk
instead of installing again. Firmware also retains `scsi0` as a fallback after
`net0`. **This BIOS/iPXE handoff still requires live proof on the target VM.** A
partially installed or damaged disk is not an invitation to wipe another disk;
stop, inspect the owned guest, and explicitly recreate it if appropriate.
Only activate the installer service for the explicitly verified blank disposable
target. The local-disk probe is not a durable completion marker: a later disk
boot failure can fall back to installation of the same allowlisted disk. After
SSH validation, stop the test provisioning service (or use a reviewed, guarded
OpenTofu update to set `network_boot = false`) before treating the host as
long-lived. This implementation does not promise automatic recovery of a damaged
installed guest.

The seed configures unattended installation, a hostname, a locked password,
OpenSSH, and an external public key. It selects only the explicitly identified
installation disk and reboots. No Ansible, Kubernetes, observability, or ingest
configuration is included.

## Network safety boundary

The service host must be an explicitly owned LabFleet resource, separate from
the coding-agent VM and Proxmox management. Its provisioning NIC and the target
guest NIC must share a dedicated, approved isolated segment. Neither protected
system may be attached to that segment. The coding-agent VM needs no new NIC:
SSH verification can use the service host as a jump host.

Before activation, independently verify the segment's ownership and isolation
using the approved network inventory. Interface names, private addresses, and
a `labfleet` string alone are not proof of network isolation. Do not guess an
unused VLAN or infer isolation from a bridge name. Do not enable forwarding,
bridging to management, NAT, or a DHCP relay as an implicit workaround.

The runtime must positively match the configured service hostname, ownership
marker, NIC name, NIC MAC, static address, and subnet before starting either
server. The provisioning NIC must be up, non-loopback, and not carry a default
route. The generated dnsmasq configuration uses `interface`, `bind-interfaces`,
and `listen-address`; HTTP binds a literal provisioning address, never `0.0.0.0`
or `::`. DHCP ignores unknown clients, supplies one fixed reservation, and does
not advertise a router or DNS server. HTTP rejects requests from non-target
source addresses. Changes detected by the runtime interface check stop service.

These controls are defense in depth, not a substitute for a physically or
logically isolated segment and restricted access. A spoofed client IP/MAC on an
untrusted shared LAN is not authenticated ownership. Do not run this service on
that LAN.

## Ubuntu artifacts: pin, verify, cache, serve

Pinned release: **Ubuntu Server 24.04.5 LTS, amd64**, legacy BIOS provisioning.

- ISO: `https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso`
- SHA-256: `97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8`
- Official checksum/signature files: `https://releases.ubuntu.com/24.04/SHA256SUMS`
  and `https://releases.ubuntu.com/24.04/SHA256SUMS.gpg`.

The release URL and checksum were checked against Canonical's published list.
The large ISO was **not downloaded or booted during the blocked lab test**.
Never substitute a moving daily/latest image or silently accept a new checksum.

On the approved service host, use a local cache outside Git. Download with HTTPS
and fail on HTTP errors. Authenticate Canonical's checksum file with an
independently trusted Ubuntu image-signing keyring, then compare the ISO hash
both to that authenticated list and the pin above. Follow
[Canonical's image verification procedure](https://documentation.ubuntu.com/security/software-integrity/image-verification/).
For example, with an already authenticated keyring:

```sh
gpgv --keyring /approved/path/ubuntu-image-signing.gpg SHA256SUMS.gpg SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

Extract `casper/vmlinuz` and `casper/initrd` from **that same verified ISO**, using
an ISO-capable tool such as `bsdtar` from `libarchive-tools` on the owned service
host. Do not install extraction tools on protected bootstrap systems for this
purpose. Keep the ISO cached as `ubuntu.iso` for HTTP delivery; netboot does not
eliminate the installer's need for the complete live-server ISO.

Use `undionly.kpxe` from an authenticated distro `ipxe` package (commonly
`/usr/lib/ipxe/undionly.kpxe`). Record the package version and SHA-256 alongside
the local artifact manifest; do not download a mutable unauthenticated binary at
service startup. Kernel, initrd, ISO, and iPXE hashes must be verified before
serving. Generated artifacts, lease files, seed files, local keys, caches, and
local configuration must remain outside Git.

On the **approved owned service host only**, the relevant distro packages are
`dnsmasq-base`, `ipxe`, and `libarchive-tools`. Use `dnsmasq-base`, not a package
that automatically starts a host-wide dnsmasq service. Do not enable the distro's
generic dnsmasq unit. Package installation is not performed by this repository.

A reproducible cache preparation sequence, in a private staging directory on
that host, is:

```sh
set -eu
curl --fail --location --proto '=https' --proto-redir '=https' \
  -o ubuntu-24.04.5-live-server-amd64.iso \
  https://releases.ubuntu.com/24.04/ubuntu-24.04.5-live-server-amd64.iso
curl --fail --proto '=https' -O https://releases.ubuntu.com/24.04/SHA256SUMS
curl --fail --proto '=https' -O https://releases.ubuntu.com/24.04/SHA256SUMS.gpg
gpgv --keyring /approved/path/ubuntu-image-signing.gpg SHA256SUMS.gpg SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
printf '%s  %s\n' \
  97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8 \
  ubuntu-24.04.5-live-server-amd64.iso | sha256sum -c -
bsdtar -xOf ubuntu-24.04.5-live-server-amd64.iso casper/vmlinuz > vmlinuz
bsdtar -xOf ubuntu-24.04.5-live-server-amd64.iso casper/initrd > initrd
cp /usr/lib/ipxe/undionly.kpxe undionly.kpxe
dpkg-query -W ipxe > ipxe-package-version.txt
mv ubuntu-24.04.5-live-server-amd64.iso ubuntu.iso
python3 - <<'PY'
import hashlib, json
names = ('ubuntu.iso', 'vmlinuz', 'initrd', 'undionly.kpxe')
digests = {}
for name in names:
    with open(name, 'rb') as stream:
        digests[name] = hashlib.file_digest(stream, 'sha256').hexdigest()
with open('manifest.json', 'w') as stream:
    json.dump({'sha256': digests}, stream, indent=2)
PY
```

Run each step only if the previous one succeeds (use `set -eu` in a script).
Install the four files and manifest as ordinary non-symlink files in the configured
artifact directory, readable but not writable by the service account. Hashes for
the extracted files are generated only after authenticating the ISO; a mutable
manifest from an untrusted source does not establish provenance. Preserve the
cache, manifest, package version, and authenticated checksum evidence for repeat
tests instead of downloading new artifacts on each boot.

## Local configuration and credentials

Use the committed example as a shape reference only. Its documentation addresses,
MACs, paths, and key placeholder are not deployable lab configuration. Supply
approved local values for the service host identity, dedicated interface/MAC,
CIDR, server/target addresses, fixed target MAC, hostname, disk identity, SSH
username, public-key path, and verified artifact cache. Real local configuration
and generated output are ignored and must not be staged.

Only a validated **public Ed25519** SSH key is consumed by the seed. Keep the matching
private key in the operator's normal credential store; it is neither copied to
the service nor embedded in user-data. Do not commit a real operator public key.
The service requires no Proxmox token. VM lifecycle operations use Issue #3's
environment-only API-token approach and live ownership guard separately.

NoCloud's HTTP seed is intended only for the isolated target network. Do not put
passwords, API tokens, or private keys in it. Installed password authentication
is disabled; verify the actual SSH behavior in the acceptance test.

## Build, render, and run

From the repository root:

```sh
go build -o bin/provisionctl ./provisioning/cmd/provisionctl
go test ./provisioning/...
```

The CLI uses `--config /absolute/path/to/local.json`. `render` produces
`dnsmasq.conf`, `boot.ipxe`, `user-data`, and `meta-data` in the configured output
directory without starting listeners. `check` verifies artifacts and host safety
and runs dnsmasq's configuration test. `serve` supervises foreground dnsmasq and
the restricted HTTP server, refusing startup on failed checks and stopping both
on safety changes or termination. No subcommand installs packages, creates a
network, configures an interface, or writes the ownership marker.

```sh
bin/provisionctl render --config provisioning/config.local.json
# These two commands are ONLY for the approved owned service host:
provisionctl check --config /etc/labfleet/provisioning.json
provisionctl serve --config /etc/labfleet/provisioning.json
```

For supervised operation, the optional unit at
`provisioning/labfleet-provisioning.service` runs as `labfleet-pxe`, grants the
network capabilities dnsmasq needs, uses a read-only system view, and permits
writes only to the generated runtime directory. It intentionally does not
restart after a failed safety check. On the owned service host, an administrator
must first:

1. Verify the host and provisioning segment against the ownership inventory.
2. Create the dedicated service account; install the built binary and required
   packages. Confirm there is no generic dnsmasq service competing for ports.
3. Install root-owned local configuration, public key, and verified artifact
   cache, readable by the service account but not writable by it.
4. Prepare `/var/lib/labfleet/provisioning/runtime`, owned by `labfleet-pxe`.
5. After verifying ownership, install a regular root-owned
   `/etc/labfleet/provisioning-owned` containing exactly `labfleet` plus a newline,
   not writable by group/others. This local attestation is not automatic discovery
   and must never be installed on either protected management system.
6. Ensure the pre-existing provisioning NIC/address match local configuration,
   no default route uses it, and forwarding is disabled. Do not have this service
   reconfigure the host to force those checks to pass.
7. Run preflight with the service's runtime permissions, inspect generated
   configuration, then install/enable only the LabFleet-specific unit.

The seed disk selector is the full udev `ID_SERIAL`, not necessarily the short
serial shown by Proxmox. For QEMU SCSI disks it can include a vendor/model prefix.
The example value is illustrative. Confirm the exact value for the configured
blank disk and require a unique match; never broaden it to a wildcard or allow
the installer to choose a management disk.

## Safe VM lifecycle integration

Use the existing [OpenTofu lifecycle](../infra/opentofu/README.md), not manual VM
adoption. Copy [the placeholder VM inputs](../provisioning/vm.example.tfvars) to
an ignored local file. For the one primary disposable guest, choose a free reserved ID, a
`labfleet-` name, a fixed locally administered `02:` MAC, and `labfleet` tags.
Use the approved existing isolated bridge, not the management bridge. Set:

- `vm_count = 1`, `network_boot = true`, and initially `started = false`.
- At least `memory_mib = 8192` and `disk_gib = 20`; the live ISO, initrd, and
  installer need substantially more RAM than the Issue #3 blank-VM defaults.
- `vm_mac_addresses` to the single allowlisted MAC.
- `disk_serial_prefix` to a LabFleet value such as `labfleet-pxe`; OpenTofu
  appends the selected VM ID. Ensure the installer's exact udev disk identity is
  selected rather than blindly choosing the largest/first disk.

Review and guard every saved plan. Verify live ownership, blank disk, MAC,
bridge, and `net0;scsi0` boot order before powering on. Do not change the service
host or protected resources as part of a target-VM apply. Start the guest only
after service binding and network isolation have been verified.

## Live acceptance and repeatability procedure

This procedure is **pending**, not an account of a completed live test.

1. Record protected resource configuration and network baselines privately.
   Confirm an owned service host and dedicated network; otherwise stop.
2. Validate the rendered dnsmasq configuration with `dnsmasq --test`, check
   cloud-init/autoinstall syntax, verify artifact hashes, and run service
   preflight. Inspect listeners after startup: only the provisioning NIC/address.
3. From the allowed guest network, check HTTP responses for the iPXE script,
   kernel, initrd, ISO, and NoCloud seed. Confirm other source addresses are denied.
4. Start exactly one owned blank guest. Record DHCP discover/offer, the intended
   server identifier, TFTP transfer, iPXE HTTP requests, and kernel/initrd/ISO/seed
   retrieval. Retain raw packet captures/logs privately; sanitize published proof.
5. Observe the console: autoinstall must start and finish without keyboard input.
   Record installer completion and reboot, followed by Ubuntu boot from disk.
6. SSH using the externally held private key, through the owned service host if
   necessary. Require successful key authentication and verify `hostname`,
   `/etc/os-release`, and `cloud-init status --wait`. Never disable SSH host-key
   checking blindly; use a per-test known-hosts file with verified fingerprints.
7. During the full test, passively capture DHCP on the provisioning and service
   host management NICs. Prove expected responses on provisioning and no DHCP
   offers from this server on management. Also confirm no protected host uses
   the target lease or receives this DHCP service. Inspect network inventory for
   unintended relay/bridge paths; absence of one packet alone is not proof.
8. Guard and apply the target's destroy plan. Verify only that VM and its disk
   disappeared; compare all protected configuration baselines.
9. Recreate the same blank target with the same approved identity/configuration.
   Repeat PXE and autoinstall; preferably complete the second SSH check. Account
   for the deliberately new SSH host key after recreating the guest.
10. Destroy the final disposable target, stop test services, and verify no test
    workload remains. Do not destroy a long-lived service host without separate
    explicit ownership and lifecycle authority.

## Troubleshooting and nested-virtualization limitations

| Stage | Checks |
| --- | --- |
| Preflight | Exact host/ownership marker, interface MAC, static address/prefix, link state, no provisioning default route; never bypass a failed check |
| DHCP | Correct isolated L2 segment, MAC allowlist, fixed lease, no competing server; inspect logs/capture, not host-wide DHCP changes |
| TFTP | UDP reachability on the dedicated NIC, accessible verified `undionly.kpxe`, service-account permissions; firmware must use legacy BIOS |
| iPXE | User-class chain detection, script URL, local-disk fallback, correctly passed NoCloud semicolon/trailing slash; inspect effective kernel command line |
| HTTP | Bound address, allowed client source, exact paths, content length/range support, cache hashes; do not expose a generic directory server |
| Installer | Sufficient RAM for ISO+initrd, exact disk identity, accessible NoCloud files, no interactive sections, offline package availability; inspect installer logs |
| Reboot | Bootable GRUB disk, BIOS drive mapping, disk-first iPXE attempt; never automatically wipe a partially installed guest to hide a boot failure |
| SSH | Completed reboot/cloud-init, hostname, public key, lease, jump path, verified host key; no password fallback or firewall disabling |

The outer host must expose hardware virtualization to nested Proxmox. Double
virtualization increases install time and storage/memory pressure. This initial
path targets amd64 SeaBIOS, not UEFI, Secure Boot, ARM, or arbitrary bare metal.
The isolated segment has no advertised Internet gateway: verify that the pinned
ISO contains required installation packages, rather than quietly routing through
management. Boot, offline installation, disk handoff, and SSH remain live-test
limitations until the required isolated lab resources exist.

## Sanitized validation evidence

No disposable VM was created for Issue #4. `labfleet-issue4-test-01` is the
**example target name**, not a successfully provisioned machine. No DHCP/PXE
runtime was launched, no provisioning NIC was added, and no Proxmox configuration
was changed. There are no installer logs, guest SSH results, or packet captures
to present as live evidence. The existing VM and management network were only
inspected through read-only API calls.

Offline checks performed with the installed system toolchain:

- Go 1.26.0: `go build ./...`, `go test ./...`, `go test -race ./...`, and all
  four Make targets passed. These include deterministic rendering, host/interface
  rejection, an early CLI startup refusal, key parsing, artifact pin/hash checks,
  and HTTP GET/HEAD/range/source/path tests with synthetic data.
- OpenTofu 1.12.6: formatting, initialization, validation, and **13 mocked test
  runs** passed. No live apply was attempted for this issue.
- dnsmasq 2.92: `--test` accepted the rendered configuration. This does not start
  DHCP or TFTP and is not a packet-isolation test.
- `cloud-init schema` accepted the rendered cloud-config. The unprivileged CLI
  emitted warnings while attempting to inspect its protected local datasource;
  it was not elevated or used to change that datasource. The autoinstall mapping
  also passed JSON Schema validation against Canonical Subiquity schema blob
  `4d5ef49879a19b30a0875cee0e4ce3f8e05e8b84`. Neither check boots the pinned ISO.
- iPXE script structure, local-disk fallback, and NoCloud kernel arguments were
  checked statically. No iPXE firmware execution was observed.
- `systemd-analyze verify` accepted a temporary copy of the unit referencing the
  locally built binary. The unit was not installed or started.
- `git diff --check` passed. Generated files and synthetic test keys remained in
  private temporary paths outside the repository.

Issue acceptance status (FAIL here means **blocked/unverified**, not an observed
failed installation):

| Acceptance criterion | Status |
| --- | --- |
| Blank LabFleet VM PXE boots | FAIL — no safe live test network/host |
| Ubuntu installation requires no input | FAIL — no live installation |
| Installed VM reboots | FAIL — no installed target |
| VM reachable over SSH | FAIL — no installed target |
| Hostname assigned automatically | FAIL — rendered only, not observed in a guest |
| SSH key authentication works | FAIL — rendered only, not exercised |
| Destroy/recreate provisioning is repeatable | FAIL — live sequence blocked |
| DHCP/PXE stays inside isolated network | FAIL — live traffic isolation unverified; no DHCP was started |
| Coding-agent VM remains unaffected | PASS — no infrastructure or host-configuration changes |
| Proxmox management remains unaffected | PASS — read-only discovery only |
| No credentials committed | PASS — only placeholders and runtime-generated test keys |

Do not interpret passing CI or schema tests as completion of these live criteria.
Keep the PR in draft and Issue #4 open until an owned service host and isolated
network are available and the full acceptance procedure has been demonstrated.
