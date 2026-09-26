mock_provider "proxmox" {}

variables {
  proxmox_node              = "example-node"
  datastore_id              = "example-storage"
  network_bridge            = "pxe-bridge"
  management_network_bridge = "management-bridge"
  ownership_confirmed       = true
}

run "six_cluster_vms_are_protected_and_pxe_ready" {
  command = plan

  assert {
    condition     = length(proxmox_virtual_environment_vm.labfleet) == 6
    error_message = "The cluster root must plan six VMs."
  }
  assert {
    condition     = [for vm in proxmox_virtual_environment_vm.labfleet : vm.vm_id] == [930081, 930082, 930083, 930084, 930085, 930086]
    error_message = "Only the explicit LabFleet-reserved IDs may be managed."
  }
  assert {
    condition     = alltrue([for vm in proxmox_virtual_environment_vm.labfleet : contains(vm.tags, "labfleet") && vm.started == false && vm.cpu[0].cores == 4 && vm.memory[0].dedicated == 16384 && vm.disk[0].size == 32 && vm.agent[0].enabled])
    error_message = "VMs must retain ownership tags, stopped initial state, and required shape."
  }
  assert {
    condition     = alltrue([for vm in proxmox_virtual_environment_vm.labfleet : vm.network_device[0].bridge == "management-bridge" && vm.network_device[1].bridge == "pxe-bridge" && vm.boot_order == tolist(["net1", "scsi0"])])
    error_message = "Management/PXE NIC ordering and PXE boot must remain explicit."
  }
  assert {
    condition     = length(output.fleet) == 6 && output.fleet[0].role == "control-plane" && output.fleet[3].role == "worker"
    error_message = "Fleet output must expose all VMs with their roles."
  }
}

run "ownership_confirmation_is_required" {
  command = plan
  variables {
    ownership_confirmed = false
  }
  expect_failures = [var.ownership_confirmed]
}
