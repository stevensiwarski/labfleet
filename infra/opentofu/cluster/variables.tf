variable "proxmox_node" {
  description = "Existing nested Proxmox node for the disposable LabFleet cluster."
  type        = string
}

variable "datastore_id" {
  description = "Existing datastore for blank VM disks."
  type        = string
}

variable "network_bridge" {
  description = "Existing isolated PXE/provisioning bridge."
  type        = string
}

variable "management_network_bridge" {
  description = "Existing management bridge (NIC0); PXE uses network_bridge (NIC1)."
  type        = string
  validation {
    condition     = var.management_network_bridge != var.network_bridge
    error_message = "Management bridge must be distinct from the provisioning bridge."
  }
}

variable "started" {
  description = "Explicitly start the six VMs after provisioning services are ready."
  type        = bool
  default     = false
}

variable "tls_insecure" {
  description = "Use only for an explicitly authorized lab with an untrusted Proxmox certificate."
  type        = bool
  default     = false
}

variable "ownership_confirmed" {
  description = "Explicit confirmation that reserved VM IDs 930081..930086 are LabFleet-owned and available."
  type        = bool
  default     = false
  validation {
    condition     = var.ownership_confirmed
    error_message = "Set ownership_confirmed=true only after verifying all reserved VM IDs belong to this LabFleet deployment or are unused."
  }
}

locals {
}
