# Placeholders only. Copy to ignored local.tfvars and select actual lab resources.
# Endpoint and token are provided through environment variables, not this file.
proxmox_node    = "NESTED-NODE-NAME"
datastore_id    = "LAB_DATASTORE"
network_bridge  = "LAB_BRIDGE"
first_vm_id     = 930000 # Example only: independently reserve and verify a free range.
vm_name_prefix  = "labfleet-disposable-test"
vm_count        = 1
vcpu            = 1
memory_mib      = 512
disk_gib        = 4
network_boot    = true
started         = false
ownership_tag   = "labfleet"
additional_tags = ["disposable"]
