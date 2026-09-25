terraform {
  required_version = ">= 1.11.0, < 2.0.0"
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "= 0.114.0"
    }
  }
}

provider "proxmox" {
  # Endpoint/token are injected through PROXMOX_VE_* environment variables.
  insecure = var.tls_insecure
}

# Simple SDN has no physical uplink. There is deliberately no subnet, gateway,
# host address, SNAT, DHCP, or resource managing the existing management bridge.
resource "proxmox_sdn_zone_simple" "labfleet" {
  id    = "labfleet"
  nodes = [var.proxmox_node]
  lifecycle { prevent_destroy = true }
}

resource "proxmox_sdn_vnet" "labfleet" {
  id    = "lfpxe4"
  zone  = proxmox_sdn_zone_simple.labfleet.id
  alias = "labfleet-provisioning"
  lifecycle { prevent_destroy = true }
}

# SDN apply is cluster-wide. Stage network first, inspect ALL pending SDN and
# node-network changes against the baseline, then explicitly advance the stage.
resource "proxmox_sdn_applier" "labfleet" {
  count      = var.stage == "network" ? 0 : 1
  on_create  = true
  on_destroy = false
  depends_on = [proxmox_sdn_vnet.labfleet]
  lifecycle { prevent_destroy = true }
}

resource "proxmox_download_file" "ubuntu" {
  count              = var.stage == "provisioner" ? 1 : 0
  node_name          = var.proxmox_node
  datastore_id       = var.artifact_datastore
  content_type       = "import"
  file_name          = "labfleet-noble-20260911.qcow2"
  url                = "https://cloud-images.ubuntu.com/releases/24.04/release-20260911/ubuntu-24.04-server-cloudimg-amd64.img"
  checksum_algorithm = "sha256"
  checksum           = "612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354"
  overwrite          = false
  lifecycle { prevent_destroy = true }
}

# A locally rendered NoCloud ISO avoids SSH/snippet uploads to the hypervisor.
resource "proxmox_virtual_environment_file" "seed" {
  count        = var.stage == "provisioner" ? 1 : 0
  node_name    = var.proxmox_node
  datastore_id = var.artifact_datastore
  content_type = "iso"
  overwrite    = false
  source_file {
    path      = var.seed_iso_path
    file_name = "labfleet-provisioner-seed.iso"
  }
  lifecycle { prevent_destroy = true }
}

resource "proxmox_virtual_environment_vm" "provisioner" {
  count         = var.stage == "provisioner" ? 1 : 0
  node_name     = var.proxmox_node
  vm_id         = var.vm_id
  name          = "labfleet-provisioner"
  description   = "LabFleet-owned persistent provisioning service. Issue 4."
  tags          = ["labfleet", "provisioner"]
  protection    = true
  bios          = "seabios"
  boot_order    = ["scsi0"]
  started       = true
  on_boot       = true
  scsi_hardware = "virtio-scsi-single"
  cpu {
    cores = 4
    type  = "qemu64"
  }
  memory { dedicated = 4096 }
  agent { enabled = true }
  disk {
    datastore_id = var.vm_datastore
    interface    = "scsi0"
    import_from  = proxmox_download_file.ubuntu[0].id
    size         = 32
  }
  cdrom {
    file_id   = proxmox_virtual_environment_file.seed[0].id
    interface = "ide2"
  }
  network_device {
    bridge      = var.management_bridge
    mac_address = var.management_mac
    model       = "virtio"
  }
  network_device {
    bridge      = proxmox_sdn_vnet.labfleet.id
    mac_address = var.provisioning_mac
    model       = "virtio"
  }
  serial_device {}
  operating_system { type = "l26" }
  depends_on = [proxmox_sdn_applier.labfleet]
  lifecycle { prevent_destroy = true }
}

output "provisioning_bridge" {
  value = proxmox_sdn_vnet.labfleet.id
}

output "provisioner_vm_id" {
  value = var.stage == "provisioner" ? proxmox_virtual_environment_vm.provisioner[0].vm_id : null
}
