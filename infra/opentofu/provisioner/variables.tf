variable "stage" {
  description = "Advance network -> activate -> provisioner only after reviewing the complete pending SDN/network changes."
  type        = string
  default     = "network"
  validation {
    condition     = contains(["network", "activate", "provisioner"], var.stage)
    error_message = "Use network, activate, or provisioner. Do not move a persistent deployment backwards."
  }
}
variable "proxmox_node" {
  type = string
}
variable "tls_insecure" {
  type    = bool
  default = false
}
variable "artifact_datastore" {
  description = "Existing storage that already supports import and iso. No storage configuration is modified."
  type        = string
}
variable "vm_datastore" {
  type = string
}
variable "management_bridge" {
  description = "Existing bridge for the new VM's control NIC only; this root never manages the bridge."
  type        = string
}
variable "vm_id" {
  type = number
  validation {
    condition     = floor(var.vm_id) == var.vm_id && var.vm_id >= 900000 && var.vm_id <= 999999
    error_message = "Reserve a free LabFleet VM ID in 900000..999999."
  }
}
variable "management_mac" {
  type = string
  validation {
    condition     = can(regex("^02(:[0-9a-fA-F]{2}){5}$", var.management_mac))
    error_message = "Use a locally administered unicast 02: MAC."
  }
}
variable "provisioning_mac" {
  type = string
  validation {
    condition     = can(regex("^02(:[0-9a-fA-F]{2}){5}$", var.provisioning_mac)) && lower(var.provisioning_mac) != lower(var.management_mac)
    error_message = "Use a distinct locally administered unicast 02: MAC."
  }
}
variable "seed_iso_path" {
  description = "Absolute path to the locally rendered, private NoCloud seed ISO. No private keys or API tokens belong in this ISO."
  type        = string
  default     = null
  validation {
    condition     = var.stage != "provisioner" || (var.seed_iso_path != null && can(regex("^/.*\\.iso$", var.seed_iso_path)))
    error_message = "The provisioner stage requires an absolute .iso seed path."
  }
}
