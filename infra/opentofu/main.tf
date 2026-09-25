provider "proxmox" {
  # API token comes only from PROXMOX_VE_API_TOKEN, never from configuration.
  # A null endpoint uses PROXMOX_VE_ENDPOINT.
  endpoint = var.proxmox_endpoint
  insecure = var.tls_insecure
}

resource "proxmox_virtual_environment_vm" "labfleet" {
  count = var.vm_count

  node_name   = var.proxmox_node
  vm_id       = var.first_vm_id + count.index
  name        = "${var.vm_name_prefix}-${format("%02d", count.index + 1)}"
  description = "Disposable LabFleet-managed blank VM. Managed by OpenTofu."
  tags        = sort(tolist(setunion(var.additional_tags, toset([var.ownership_tag]))))

  bios          = "seabios"
  machine       = "pc"
  scsi_hardware = "virtio-scsi-single"
  boot_order    = var.network_boot ? ["net0", "scsi0"] : ["scsi0"]
  started       = var.started
  on_boot       = false

  # Blank guests have no OS to answer an ACPI shutdown request.
  stop_on_destroy                      = true
  purge_on_destroy                     = false
  delete_unreferenced_disks_on_destroy = false

  agent {
    enabled = false
  }

  cpu {
    cores = var.vcpu
    type  = "qemu64"
  }

  memory {
    dedicated = var.memory_mib
  }

  disk {
    datastore_id = var.datastore_id
    interface    = "scsi0"
    size         = var.disk_gib
    file_format  = "raw"
    serial       = var.disk_serial_prefix == null ? null : "${var.disk_serial_prefix}-${var.first_vm_id + count.index}"
  }

  network_device {
    bridge      = var.network_bridge
    model       = "virtio"
    mac_address = length(var.vm_mac_addresses) == 0 ? null : var.vm_mac_addresses[count.index]
  }
}
