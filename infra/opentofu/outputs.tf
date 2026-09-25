output "managed_vms" {
  description = "Identifiers and boot configuration of this state's disposable fleet only."
  value = [for vm in proxmox_virtual_environment_vm.labfleet : {
    vm_id      = vm.vm_id
    name       = vm.name
    tags       = vm.tags
    boot_order = vm.boot_order
  }]
}
