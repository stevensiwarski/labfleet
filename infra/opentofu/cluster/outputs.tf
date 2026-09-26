output "fleet" {
  description = "PXE inventory inputs for the six reserved disposable Issue 8 cluster VMs."
  value = [for i, vm in proxmox_virtual_environment_vm.labfleet : {
    name           = vm.name
    vm_id          = vm.vm_id
    mac            = local.provisioning_macs[i]
    management_mac = local.management_macs[i]
    role           = local.roles[i]
    tags           = vm.tags
  }]
}
