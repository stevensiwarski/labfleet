# Documentation placeholders only. Never use the management bridge for PXE.
# Copy outside tracked configuration and verify the reserved ID is free.
proxmox_node       = "NESTED-NODE-NAME"
datastore_id       = "LAB_DATASTORE"
network_bridge     = "ISOLATED_LAB_BRIDGE"
first_vm_id        = 930004
vm_name_prefix     = "labfleet-issue4-test"
vm_count           = 1
vcpu               = 2
memory_mib         = 16384
disk_gib           = 20
network_boot       = true
started            = false
ownership_tag      = "labfleet"
additional_tags    = ["disposable", "pxe-test"]
vm_mac_addresses   = ["02:00:00:00:00:04"]
disk_serial_prefix = "labfleet-pxe"
