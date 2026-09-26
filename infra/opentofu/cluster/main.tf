provider "proxmox" {
  # Credentials are supplied only through PROXMOX_VE_API_TOKEN.
  endpoint = null
  insecure = var.tls_insecure
}

locals {
  vm_ids = [930081, 930082, 930083, 930084, 930085, 930086]
  names  = ["labfleet-cp-01", "labfleet-cp-02", "labfleet-cp-03", "labfleet-worker-01", "labfleet-worker-02", "labfleet-worker-03"]
  roles  = ["control-plane", "control-plane", "control-plane", "worker", "worker", "worker"]
  management_macs = [
    "02:93:00:01:80:81", "02:93:00:01:80:82", "02:93:00:01:80:83",
    "02:93:00:01:80:84", "02:93:00:01:80:85", "02:93:00:01:80:86",
  ]
  provisioning_macs = [
    "02:93:00:00:80:81", "02:93:00:00:80:82", "02:93:00:00:80:83",
    "02:93:00:00:80:84", "02:93:00:00:80:85", "02:93:00:00:80:86",
  ]
}

resource "proxmox_virtual_environment_vm" "labfleet" {
  count = 6

  node_name   = var.proxmox_node
  vm_id       = local.vm_ids[count.index]
  name        = local.names[count.index]
  description = "Disposable LabFleet-managed blank VM. Managed by OpenTofu."
  tags        = sort([local.roles[count.index], "disposable", "issue8", "labfleet"])

  bios          = "seabios"
  machine       = "pc"
  scsi_hardware = "virtio-scsi-single"
  boot_order    = ["net1", "scsi0"]
  started       = var.started
  on_boot       = false

  stop_on_destroy                      = true
  purge_on_destroy                     = false
  delete_unreferenced_disks_on_destroy = false

  agent {
    enabled = true
  }

  cpu {
    cores = 4
    type  = "qemu64"
  }

  memory {
    dedicated = 16384
  }

  disk {
    datastore_id = var.datastore_id
    interface    = "scsi0"
    size         = 32
    file_format  = "raw"
    serial       = "labfleet-k8s-${local.vm_ids[count.index]}"
  }

  network_device {
    bridge      = var.management_network_bridge
    model       = "virtio"
    mac_address = local.management_macs[count.index]
  }

  network_device {
    bridge      = var.network_bridge
    model       = "virtio"
    mac_address = local.provisioning_macs[count.index]
  }
}
