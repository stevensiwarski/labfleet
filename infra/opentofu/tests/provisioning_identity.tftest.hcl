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
