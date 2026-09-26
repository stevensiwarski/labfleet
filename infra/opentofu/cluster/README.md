# Issue #8 disposable Kubernetes fleet

This root owns exactly six blank guests: control planes `930081..930083` and
workers `930084..930086`. It does not manage the hypervisor, bridges, persistent
provisioner or Kubernetes objects. See the
[cluster lifecycle guide](../../../docs/kubernetes-bootstrap.md).

Each guest has 4 vCPU, 16 GiB RAM, a blank 32 GiB SCSI disk, explicit management
and provisioning MACs, and `labfleet;disposable;issue8` plus its role tag.
Management NIC0 references an existing bridge; provisioning NIC1 references the
existing isolated network and is the PXE boot device. `started` defaults false.
The 16 GiB setting follows the earlier measured RAM-backed ISO installation
failure at 8 GiB, not a Kubernetes runtime minimum.

Required private inputs: `proxmox_node`, `datastore_id`, `network_bridge`,
`management_network_bridge`, and `ownership_confirmed=true` after independent
ID/ownership verification. API credentials come only from provider environment
variables. TLS verification is enabled unless explicitly overridden for the lab.

The root deliberately retains `proxmox_virtual_environment_vm.labfleet[index]`
resource addresses supported by the existing `fleetctl tofu-check` guard; it does
not weaken that guard to accept arbitrary module addresses. Always check the
saved plan before applying it, including starts and destroys. `fleet` output
provides the six names, IDs, role tags and both MACs for PXE and discovery.

Keep state, real tfvars, plans, inventories and kubeconfigs private. Do not delete
state while the retained final cluster exists. Destruction must verify exactly
the six owned guests and their disks were removed, leaving protected resources
and the persistent network intact. No automatic reboot/upgrade is implemented.
