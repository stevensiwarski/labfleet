mock_provider "proxmox" {}

variables {
  proxmox_node       = "example-node"
  datastore_id       = "example-storage"
  network_bridge     = "isolated-example"
  first_vm_id        = 930004
  vm_mac_addresses   = ["02:00:00:00:00:04"]
  disk_serial_prefix = "labfleet-pxe"
}

run "explicit_provisioning_identity" {
  command = plan
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].network_device[0].mac_address == "02:00:00:00:00:04"
    error_message = "DHCP reservation requires the explicit target MAC."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].disk[0].serial == "labfleet-pxe-930004"
    error_message = "Autoinstall must select the explicit disposable disk serial."
  }
  assert {
    condition     = contains(proxmox_virtual_environment_vm.labfleet[0].tags, "labfleet") && proxmox_virtual_environment_vm.labfleet[0].boot_order == tolist(["net0", "scsi0"])
    error_message = "Provisioning must retain ownership and network-first firmware boot."
  }
}

run "dual_nic_keeps_provisioning_identity_on_net1" {
  command = plan
  variables {
    management_network_bridge = "management-example"
    management_mac_addresses  = ["02:00:00:00:10:04"]
    guest_agent_enabled       = true
  }
  assert {
    condition     = length(proxmox_virtual_environment_vm.labfleet[0].network_device) == 2 && proxmox_virtual_environment_vm.labfleet[0].network_device[0].bridge == "management-example" && proxmox_virtual_environment_vm.labfleet[0].network_device[0].mac_address == "02:00:00:00:10:04"
    error_message = "NIC0 must be management with its optional explicit MAC."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].network_device[1].bridge == "isolated-example" && proxmox_virtual_environment_vm.labfleet[0].network_device[1].mac_address == "02:00:00:00:00:04" && proxmox_virtual_environment_vm.labfleet[0].boot_order == tolist(["net1", "scsi0"])
    error_message = "NIC1 must retain provisioning identity and receive PXE boot priority."
  }
  assert {
    condition     = proxmox_virtual_environment_vm.labfleet[0].agent[0].enabled
    error_message = "Guest agent must be enabled when explicitly opted in."
  }
}

run "reject_same_management_and_provisioning_bridge" {
  command = plan
  variables { management_network_bridge = "isolated-example" }
  expect_failures = [var.management_network_bridge]
}

run "reject_management_mac_without_bridge" {
  command = plan
  variables { management_mac_addresses = ["02:00:00:00:10:04"] }
  expect_failures = [var.management_mac_addresses]
}

run "reject_duplicate_cross_nic_macs" {
  command = plan
  variables {
    management_network_bridge = "management-example"
    management_mac_addresses  = ["02:00:00:00:00:04"]
  }
  expect_failures = [var.management_mac_addresses]
}

run "reject_duplicate_macs" {
  command = plan
  variables {
    vm_count         = 2
    vm_mac_addresses = ["02:00:00:00:00:04", "02:00:00:00:00:04"]
  }
  expect_failures = [var.vm_mac_addresses]
}

run "reject_short_mac_list" {
  command = plan
  variables {
    vm_count = 2
  }
  expect_failures = [var.vm_mac_addresses]
}

run "reject_unowned_disk_serial" {
  command = plan
  variables {
    disk_serial_prefix = "unmanaged"
  }
  expect_failures = [var.disk_serial_prefix]
}

run "maximum_disk_serial_length" {
  command = plan
  variables {
    disk_serial_prefix = "labfleet-test"
  }
  assert {
    condition     = length(proxmox_virtual_environment_vm.labfleet[0].disk[0].serial) == 20
    error_message = "The longest allowed serial must fit Proxmox's 20-byte limit."
  }
}

run "reject_overlong_disk_serial" {
  command = plan
  variables {
    disk_serial_prefix = "labfleet-tests"
  }
  expect_failures = [var.disk_serial_prefix]
}
run "reject_cross_vm_mac_reuse" {
  command = plan
  variables {
    vm_count                  = 2
    management_network_bridge = "management-test"
    vm_mac_addresses          = ["02:00:00:00:00:0a", "02:00:00:00:00:0b"]
    management_mac_addresses  = ["02:00:00:00:00:0B", "02:00:00:00:00:0c"]
  }
  expect_failures = [var.management_mac_addresses]
}
