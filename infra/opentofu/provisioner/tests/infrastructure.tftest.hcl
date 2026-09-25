mock_provider "proxmox" {
  mock_resource "proxmox_download_file" {
    defaults = { id = "example-artifacts:import/labfleet-cloud.qcow2" }
  }
  mock_resource "proxmox_virtual_environment_file" {
    defaults = { id = "example-artifacts:iso/labfleet-seed.iso" }
  }
}

variables {
  proxmox_node       = "example-node"
  artifact_datastore = "example-artifacts"
  vm_datastore       = "example-disks"
  management_bridge  = "example-mgmt"
  vm_id              = 930040
  management_mac     = "02:00:00:00:04:01"
  provisioning_mac   = "02:00:00:00:04:02"
}

run "definitions_only" {
  command = plan
  assert {
    condition     = proxmox_sdn_zone_simple.labfleet.id == "labfleet" && proxmox_sdn_vnet.labfleet.zone == "labfleet"
    error_message = "Both new SDN objects must have explicit LabFleet ownership."
  }
  assert {
    condition     = length(proxmox_sdn_applier.labfleet) == 0 && length(proxmox_virtual_environment_vm.provisioner) == 0
    error_message = "Default stage must not activate SDN or create guests before pending-change review."
  }
}

run "activation_only" {
  command = plan
  variables { stage = "activate" }
  assert {
    condition     = proxmox_sdn_applier.labfleet[0].on_create && !proxmox_sdn_applier.labfleet[0].on_destroy && length(proxmox_virtual_environment_vm.provisioner) == 0
    error_message = "Activation must be explicit and not silently apply cluster SDN during teardown."
  }
}

run "owned_dual_nic_provisioner" {
  command = plan
  variables {
    stage         = "provisioner"
    seed_iso_path = "/example/labfleet-seed.iso"
  }
  assert {
    condition     = contains(proxmox_virtual_environment_vm.provisioner[0].tags, "labfleet") && proxmox_virtual_environment_vm.provisioner[0].protection
    error_message = "Persistent provisioner must be owned and deletion-protected."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.provisioner[0].network_device[0].bridge == "example-mgmt" && proxmox_virtual_environment_vm.provisioner[0].network_device[1].bridge == "lfpxe4"
    error_message = "Control and provisioning NICs must remain distinct."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.provisioner[0].boot_order == tolist(["scsi0"]) && proxmox_virtual_environment_vm.provisioner[0].disk[0].size == 32
    error_message = "Provisioner must boot its own cloud-image disk, not PXE."
  }
}

run "reject_unreserved_vm_id" {
  command = plan
  variables { vm_id = 100 }
  expect_failures = [var.vm_id]
}

run "reject_duplicate_nic_macs" {
  command = plan
  variables { provisioning_mac = "02:00:00:00:04:01" }
  expect_failures = [var.provisioning_mac]
}
