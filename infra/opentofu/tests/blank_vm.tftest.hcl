# Mocked tests never contact Proxmox or create infrastructure.
mock_provider "proxmox" {}

variables {
  proxmox_node   = "example-node"
  datastore_id   = "example-storage"
  network_bridge = "example-bridge"
  first_vm_id    = 930000
}

run "blank_network_boot_vm" {
  command = plan

  assert {
    condition     = length(proxmox_virtual_environment_vm.labfleet) == 1
    error_message = "Default validation must create exactly one VM."
  }
  assert {
    condition     = contains(proxmox_virtual_environment_vm.labfleet[0].tags, "labfleet")
    error_message = "Ownership tag must always be present."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].boot_order == tolist(["net0", "scsi0"])
    error_message = "Network boot must come first."
  }
  assert {
    condition     = !proxmox_virtual_environment_vm.labfleet[0].started && !proxmox_virtual_environment_vm.labfleet[0].on_boot
    error_message = "Starting the guest must be opt-in."
  }
  assert {
    condition     = !proxmox_virtual_environment_vm.labfleet[0].agent[0].enabled
    error_message = "Guest agent must remain disabled by default."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].disk[0].size == 4 && proxmox_virtual_environment_vm.labfleet[0].disk[0].interface == "scsi0"
    error_message = "Expected a small blank SCSI disk."
  }
  assert {
    condition     = length(proxmox_virtual_environment_vm.labfleet[0].clone) == 0 && length(proxmox_virtual_environment_vm.labfleet[0].initialization) == 0 && length(proxmox_virtual_environment_vm.labfleet[0].cdrom) == 0
    error_message = "Blank guests must have no clone, cloud-init, or installation media."
  }
}

run "configurable_fleet" {
  command = plan
  variables {
    vm_count        = 2
    vm_name_prefix  = "labfleet-example"
    vcpu            = 2
    memory_mib      = 1024
    disk_gib        = 8
    network_boot    = false
    additional_tags = ["test"]
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[1].vm_id == 930001 && proxmox_virtual_environment_vm.labfleet[1].name == "labfleet-example-02"
    error_message = "VM IDs and names must be deterministic and unique."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].cpu[0].cores == 2 && proxmox_virtual_environment_vm.labfleet[0].memory[0].dedicated == 1024 && proxmox_virtual_environment_vm.labfleet[0].disk[0].size == 8
    error_message = "Resource settings must be configurable."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].boot_order == tolist(["scsi0"]) && contains(proxmox_virtual_environment_vm.labfleet[0].tags, "test") && contains(proxmox_virtual_environment_vm.labfleet[0].tags, "labfleet")
    error_message = "Optional settings must not remove ownership."
  }
}

run "reject_unowned_tag" {
  command = plan
  variables { ownership_tag = "unmanaged" }
  expect_failures = [var.ownership_tag]
}

run "reject_bootstrap_id_range" {
  command = plan
  variables { first_vm_id = 100 }
  expect_failures = [var.first_vm_id]
}

run "reject_unowned_name" {
  command = plan
  variables { vm_name_prefix = "unmanaged" }
  expect_failures = [var.vm_name_prefix]
}

run "reject_empty_fleet" {
  command = plan
  variables { vm_count = 0 }
  expect_failures = [var.vm_count]
}

run "reject_overflowing_id_range" {
  command = plan
  variables {
    first_vm_id = 999999
    vm_count    = 2
  }
  expect_failures = [var.first_vm_id]
}
