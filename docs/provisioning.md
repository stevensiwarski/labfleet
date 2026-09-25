# Isolated Ubuntu PXE provisioning

## Status and automated infrastructure

Issue #4 includes an API/OpenTofu-created Simple SDN zone `labfleet`, isolated
VNet `lfpxe4` (alias `labfleet-provisioning`), and a dedicated
`labfleet-provisioner` Ubuntu VM tagged `labfleet`. No manual GUI setup or
operator-created network/VM is required. See the staged
[infrastructure/bootstrap procedure](../infra/opentofu/provisioner/README.md).
Two complete unattended PXE → install → disk reboot → SSH cycles passed, with
guarded destruction and fresh blank-disk recreation between them.

The provisioner has 4 vCPU, 4 GiB RAM and a 32 GiB disk. Its `mgmt0` NIC obtains a
control address on the existing management LAN; `prov0` has a local-configured
static address on a separate private /24, no gateway and no DNS. Forwarding is
disabled. The SDN VNet has no physical uplink, host address, NAT, or gateway.
Only the provisioner and one disposable target attach to it. The isolated
installation does not depend on management-LAN connectivity; management is used
only for provisioner bootstrap/artifact downloads and SSH control.

The protected coding-agent VM, existing management bridge/interfaces, management
IP/default gateway, and unmanaged guests are never managed by these roots. SDN
activation is cluster-wide: compare all pending changes to a private baseline
and require that only the new owned definitions differ before applying.

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
        +--- disk-select (static Go helper) --- verify exact owned disk identity
                                              |
                                         install to identified disk
                                              |
                                         reboot → local disk → SSH
```

The dnsmasq process provides a static lease to exactly one configured target MAC
and serves a BIOS iPXE image using TFTP. An iPXE user-class match breaks the chain
loop: firmware receives `undionly.kpxe`; iPXE receives an HTTP boot script. HTTP
serves only named boot artifacts and that target's NoCloud seed. There is no DNS
service, general DHCP pool, management-network DHCP, or network reconfiguration
by the running PXE service.

The iPXE script first attempts `sanboot --no-describe --drive 0x80`. A genuinely
blank disk falls through to the installer. Once Ubuntu has installed a bootable
disk, a subsequent network-first firmware boot should hand off to that disk
instead of installing again. Firmware also retains `scsi0` as a fallback after
`net0`. The live run verified this BIOS/iPXE handoff on the disposable target. A
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
OpenSSH, and an external public key. A static Go `disk-select` early-command
verifies the exact LabFleet SCSI by-id link, its resolved whole block device, and
its `ID_SCSI_SERIAL`. It requires a unique observed `ID_SERIAL` among all whole
disks before rewriting that single selector in `/autoinstall.yaml`. Subiquity
rereads the file after early commands. This accounts for QEMU reporting
`ID_SERIAL=0QEMU_QEMU_HARDDISK_drive-scsi0` even when its SCSI serial/by-id link
contains the configured LabFleet identity. Missing, mismatched or duplicate
identities abort; there is no first-disk, largest-disk or `/dev/sda` fallback.
The installer then installs only to that identified disk and reboots. No Ansible,
Kubernetes, observability, or ingest configuration is included.

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
The live deployment downloaded the ISO, verified its pinned SHA-256 and the
published checksum signature using Ubuntu's trusted archive keyring, and
extracted the kernel/initrd from that ISO.
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
service startup. Kernel, initrd, ISO, iPXE and disk-helper hashes must be verified before
serving. Generated artifacts, lease files, seed files, local keys, caches, and
local configuration must remain outside Git.

On the **approved owned service host only**, the relevant distro packages are
`dnsmasq-base`, `ipxe`, and `libarchive-tools`. Use `dnsmasq-base`, not a package
that automatically starts a host-wide dnsmasq service. Do not enable the distro's
generic dnsmasq unit. The provisioner's generated cloud-init seed installs these
packages automatically on that new owned VM, never on protected infrastructure.

Build the installer helper in the repository build workspace with
`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /private/path/disk-select ./provisioning/cmd/disk-select`
and transfer it to the owned service host's private staging directory. It is a
small repository-built executable, not a new distro package or installer image.
A reproducible cache preparation sequence in that staging directory is:

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
names = ('ubuntu.iso', 'vmlinuz', 'initrd', 'undionly.kpxe', 'disk-select')
digests = {}
for name in names:
    with open(name, 'rb') as stream:
        digests[name] = hashlib.file_digest(stream, 'sha256').hexdigest()
with open('manifest.json', 'w') as stream:
    json.dump({'sha256': digests}, stream, indent=2)
PY
```

Run each step only if the previous one succeeds (use `set -eu` in a script).
Install the five files and manifest as ordinary non-symlink files in the configured
artifact directory, readable but not writable by the service account. Hashes for
the extracted files are generated only after authenticating the ISO; a mutable
manifest from an untrusted source does not establish provenance. Preserve the
cache, manifest, package version, and authenticated checksum evidence for repeat
tests instead of downloading new artifacts on each boot.

## Local configuration and credentials

### Optional configuration-management node profile

The single-NIC, no-passwordless-sudo defaults remain unchanged. For a disposable
node that needs package/DNS/NTP access and unattended Ansible:

- OpenTofu: set `management_network_bridge` to an existing management bridge,
  `management_mac_addresses` to the new guest's NIC0 MAC, and
  `guest_agent_enabled = true`. Provisioning identity remains in
  `network_bridge`/`vm_mac_addresses` on NIC1; firmware boots `net1;scsi0`.
- PXE JSON: supply that NIC0 MAC as `management_mac`; `target_mac` remains the
  isolated PXE NIC MAC. The iPXE command line uses `BOOTIF` to select only that
  MAC for initramfs DHCP. This behavior was checked in the pinned ISO's initrd.
- Autoinstall names the interfaces `mgmt0` and `prov0`. Management uses DHCP with
  route metric 100. Provisioning rejects DHCP routes/DNS, disables IPv6 RA/DHCPv6,
  and advertises no default route. No routing/NAT is enabled on the provisioner.
- The dual-NIC seed installs/enables `qemu-guest-agent` for authenticated API
  discovery of the management address and SSH host key.
- Explicit `passwordless_sudo: true` creates a root-owned mode-0440 sudoers
  drop-in for the configured node user, validated with `visudo`. This is required
  for unattended Ansible's apt/systemd/kernel/sysctl/file operations when the user
  has a locked password. It grants root-equivalent local administration **only on
  the opt-in disposable node**, never on the controller or provisioner. SSH
  remains key-only and remote root access stays disabled.

When changing the target MAC, use a distinct unused isolated reservation or
review the stopped service's obsolete lease explicitly. The existing infinite
lease must not be silently reused by another MAC. Restore the normal local
profile and stop test PXE services after validation. Real addresses, keys and
profile files remain private.

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
restart after a failed safety check. The automated provisioner bootstrap already
creates its account, directories, marker, NIC configuration, and packages. The
deployment sequence (executed over SSH on that owned host) must verify:

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

The local `disk_serial` input is the exact QEMU SCSI by-id suffix,
`SQEMU_QEMU_HARDDISK_` followed by the configured short LabFleet disk serial
in the validated lab (the helper also supports the `0QEMU_` by-id variant, but
still requires the exact configured link; it never substitutes between them).
The helper independently verifies this link and the short `ID_SCSI_SERIAL`
before translating it to Subiquity's probed `ID_SERIAL`. It rejects ambiguous
whole-disk identities. A live attempt without this translation failed closed
before partitioning; do not work around that failure by weakening the selector.

## Safe VM lifecycle integration

Use the existing [OpenTofu lifecycle](../infra/opentofu/README.md), not manual VM
adoption. Copy [the placeholder VM inputs](../provisioning/vm.example.tfvars) to
an ignored local file. For the one primary disposable guest, choose a free reserved ID, a
`labfleet-` name, a fixed locally administered `02:` MAC, and `labfleet` tags.
Use the approved existing isolated bridge, not the management bridge. Set:

- `vm_count = 1`, `network_boot = true`, and initially `started = false`.
- Use `memory_mib = 16384` and `disk_gib = 20`; the live ISO, initrd, and
  installer need substantially more RAM than the Issue #3 blank-VM defaults.
  The live 8 GiB attempt exhausted the initramfs RAM-backed filesystem while
  downloading the ISO; 16 GiB is the validated target sizing, not a requirement
  for the separate provisioner VM.
- `vm_mac_addresses` to the single allowlisted MAC.
- `disk_serial_prefix` to a LabFleet value such as `labfleet-pxe`; OpenTofu
  appends the selected VM ID. Ensure the installer's exact udev disk identity is
  selected rather than blindly choosing the largest/first disk.

Review and guard every saved plan. Verify live ownership, blank disk, MAC,
bridge, and `net0;scsi0` boot order before powering on. Do not change the service
host or protected resources as part of a target-VM apply. Start the guest only
after service binding and network isolation have been verified.

## Live acceptance and repeatability procedure

Use this procedure for subsequent controlled reprovisioning. The evidence below
separately records what the Issue #4 live run observed.

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
management. The provisioner's management NIC may use Internet access for package
and artifact bootstrap; the target has no management NIC or advertised gateway.

## Sanitized validation evidence

Live infrastructure was created through reviewed OpenTofu/API operations:

- Simple SDN zone `labfleet`; VNet `lfpxe4`, alias `labfleet-provisioning`.
- Persistent `labfleet-provisioner`, VM `930040`, tags `labfleet;provisioner`.
- Exactly one disposable `labfleet-issue4-test-01`, VM `930004`, tags
  `disposable;labfleet;pxe-test`, no local installation media, fresh 20 GiB disk,
  deterministic MAC/SCSI serial, `net0;scsi0` boot order, 16 GiB RAM.
- Owned cloud-image import and NoCloud seed ISO; separate persistent and
  disposable OpenTofu state roots. State, real addresses, keys and raw evidence
  remain private and ignored, not committed.

The baseline and post-apply API comparisons showed identical protected VM
configuration/running state, existing management interface/bridge configuration,
node configuration and configured management address/default gateway. Management
API and coding-agent connectivity remained available. The VNet bridge API showed
only the target's `net0` and provisioner's `net1`, no physical uplink and no
protected VM attachment. No packages were installed on the coding-agent VM;
build-only ISO/WebSocket tools were extracted in a private temporary workspace.

Observed network evidence includes DHCP DISCOVER/OFFER/REQUEST/ACK on `prov0`,
iPXE's user class and HTTP user-agent, and HTTP requests for the script,
kernel/initrd, ISO, NoCloud seed and disk helper. Proxmox's virtio PXE ROM already
runs iPXE, so it takes the HTTP branch directly; TFTP `undionly.kpxe` remains the
configured fallback for non-iPXE BIOS firmware. A separate TFTP transfer from the
installed target returned `undionly.kpxe` with the exact verified manifest hash.
Do not label that service test as an observed TFTP firmware chain-load.

dnsmasq reports `sockets bound exclusively to interface prov0`; `ss` confirms
the DHCP socket has `%prov0` device binding. Linux represents its local address
as `0.0.0.0%prov0:67`: this is **not** an unscoped all-interface wildcard listener.
There is no management-interface DHCP server socket. HTTP is both address- and
device-bound, rejects non-target requests with 403, and both forwarding sysctls
are zero. Management-side packet captures filtered for DHCP replies from the
provisioner are retained privately with the provisioning captures. Both runs
recorded zero LabFleet DHCP replies on management. The second capture included
all management DHCP replies and checked both owned MACs and the provisioning
source IP, rather than assuming other lab DHCP servers were absent.

Live discovery caught and corrected three assumptions before any successful
installation: 8 GiB was insufficient for the ISO's RAM-backed download; the
actual SCSI by-id link uses `SQEMU_`, while Subiquity sees a different `ID_SERIAL`;
and the installer's `/run` is `noexec`. These failures stopped before disk
partitioning. Diagnostic console input was confined to those failed attempts;
successful installation used a fresh boot with no installer keyboard input.

The first complete run recorded script/kernel/initrd requests at 08:13, ISO/seed
and verified disk-helper retrieval at 08:14, and a post-install `boot.ipxe`
request at 08:17 with **no new kernel/initrd/ISO download**. The installed system
reported `Ubuntu 24.04.5 LTS`, hostname `labfleet-issue4-test-01`, ext4 root on
`/dev/sda2`, a normal `/boot/vmlinuz-6.8.0-139-generic root=UUID=…` command line,
and systemd state `running`. The retained curtin log ends with
`curtin: Installation finished.` and successful GRUB/bootloader installation.
SSH public-key authentication from the coding-agent through the pinned owned
provisioner succeeded; a password-only attempt was rejected. The target's SSH
host key was obtained through that authenticated provisioner after checking the
exclusive owned VNet membership and reserved MAC, then pinned for strict SSH
verification; its fingerprint also matched the VM console. The private key was
never copied to the provisioner or target.

The console recorded cloud-init final completion. Afterwards `cloud-init status`
reports `disabled`: `/etc/cloud/cloud-init.disabled` explicitly says
`Disabled by Ubuntu live installer after first boot.` This is the installed
image's documented state, not an unfinished cloud-init run. The provisioner,
which boots a cloud image rather than an autoinstall disk, reports `status: done`.
The installed `fleet` account has no usable password; unattended administrative
sudo is not enabled on the disposable target.

The first target was destroyed through a reviewed, ownership-guarded plan;
inventory and storage-content checks confirmed both VM and disk absent. Exactly
one fresh blank target with the same deterministic identity was then recreated
through the same guard for the repeat run.

The second run started at 08:20, downloaded the same cached installer/seed/helper,
and requested only the boot script at 08:24 for disk handoff. Its capture contains
exactly one ISO download and two boot-script requests. It again completed curtin,
rebooted to Ubuntu 24.04.5 with the expected hostname, ext4 disk root, systemd
state `running`, and successful strict key-authenticated SSH. Its filesystem UUID
and SSH host key differ from the first installation, confirming fresh state; the
second key fingerprint also matches its console. No installation keyboard input
was used in either successful run.

After the second validation, the guarded destroy removed the disposable VM and
its disk again. The final inventory contains only the original protected guest
and the persistent tagged provisioner; the isolated bridge has only the
provisioner's provisioning NIC. Baseline comparisons still pass. The test
DHCP/TFTP/HTTP service and captures are stopped, with no test listeners left.
The provisioner, isolated SDN resources, owned bootstrap artifacts and verified
installer cache remain for future explicitly scoped LabFleet work. A follow-up
plan for the persistent infrastructure was a no-op.

Offline checks performed with the installed system toolchain:

- Go 1.26.0: `go build ./...`, `go test ./...`, `go test -race ./...`, and all
  four Make targets passed. These include deterministic rendering, host/interface
  rejection, an early CLI startup refusal, key parsing, artifact pin/hash checks,
  and HTTP GET/HEAD/range/source/path tests with synthetic data.
- OpenTofu 1.12.6: formatting, initialization, validation, and **18 mocked test
  runs** passed (13 disposable lifecycle tests plus five persistent infrastructure
  tests). Live applies used saved/reviewed plans; target plans also passed the
  existing live `fleetctl tofu-check` ownership guard.
- dnsmasq 2.91 on the provisioner: `--test` accepted the deployed configuration;
  actual listener and packet evidence is reported separately above.
- `cloud-init schema` on the owned provisioner accepted the deployed cloud-config.
  The autoinstall mapping also passed JSON Schema validation against Canonical
  Subiquity schema blob
  `4d5ef49879a19b30a0875cee0e4ce3f8e05e8b84`, including the new early-command.
  The owned provisioner's own NoCloud user-data also passed `cloud-init schema`
  and completed cloud-init successfully.
- iPXE execution and artifact retrieval were observed live; disk-handoff evidence
  is tracked with the installation result below.
- `systemd-analyze verify` accepted the installed dedicated service unit. The
  service ran only on the new owned provisioner and passed runtime preflight.
- `git diff --check` passed. Generated files and synthetic test keys remained in
  private temporary paths outside the repository.

Issue acceptance status after both complete runs and final cleanup:

| Acceptance criterion | Status |
| --- | --- |
| Blank LabFleet VM PXE boots | PASS — iPXE and installer kernel/initrd/ISO retrieval observed |
| Ubuntu installation requires no input | PASS — fresh unattended run and curtin completion log |
| Installed VM reboots | PASS — disk root/kernel command line and no repeat installer download |
| VM reachable over SSH | PASS — coding-agent through owned provisioner |
| Hostname assigned automatically | PASS — `labfleet-issue4-test-01` |
| SSH key authentication works | PASS — key-only login; password-only attempt rejected |
| Destroy/recreate provisioning is repeatable | PASS — second complete fresh-disk installation and SSH validation |
| DHCP/PXE stays inside isolated network | PASS — isolated VNet membership, device-bound socket and management capture |
| Coding-agent VM remains unaffected | PASS — protected configuration unchanged and connectivity retained |
| Proxmox management remains unaffected | PASS — management baseline unchanged and API reachable |
| No credentials committed | PASS — only placeholders and runtime-generated test keys |

Passing CI or schema tests alone is not proof of these live criteria; the results
above come from the owned lab runs. PR #10 is intended for human review after
the updated CI passes, not automatic merge. No Issue #5 work is included.
