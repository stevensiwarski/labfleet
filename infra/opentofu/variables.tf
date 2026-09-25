variable "proxmox_endpoint" {
  description = "HTTPS API endpoint, without /api2/json. Prefer PROXMOX_VE_ENDPOINT."
  type        = string
  default     = null
  sensitive   = true
  validation {
    condition     = var.proxmox_endpoint == null ? true : can(regex("^https://[^/@]+/?$", var.proxmox_endpoint))
    error_message = "Use an HTTPS endpoint without credentials or an API path."
  }
}

variable "tls_insecure" {
  description = "Explicit lab-only opt-in to skip TLS verification; prefer a trusted certificate."
  type        = bool
  default     = false
}

variable "proxmox_node" {
  description = "Nested Proxmox node on which disposable VMs are created."
  type        = string
  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9.-]*$", var.proxmox_node))
    error_message = "Specify a node name, not an address or path."
  }
}

variable "vm_name_prefix" {
  description = "VM names append a two-digit index to this LabFleet-prefixed name."
  type        = string
  default     = "labfleet-test"
  validation {
    condition     = can(regex("^labfleet-[a-z0-9][a-z0-9-]{0,40}$", var.vm_name_prefix))
    error_message = "The prefix must begin labfleet- and contain lowercase DNS-label characters."
  }
}

variable "first_vm_id" {
  description = "First explicitly reserved free VM ID; all fleet IDs must be in 900000..999999."
  type        = number
  validation {
    condition     = floor(var.first_vm_id) == var.first_vm_id && var.first_vm_id >= 900000 && var.first_vm_id + var.vm_count - 1 <= 999999
    error_message = "Reserve an integer ID range within 900000..999999."
  }
}

variable "vm_count" {
  description = "Number of disposable VMs; keep initial validation to one."
  type        = number
  default     = 1
  validation {
    condition     = floor(var.vm_count) == var.vm_count && var.vm_count >= 1 && var.vm_count <= 20
    error_message = "VM count must be an integer from 1 to 20."
  }
}

variable "vcpu" {
  description = "Virtual CPU cores per VM."
  type        = number
  default     = 1
  validation {
    condition     = floor(var.vcpu) == var.vcpu && var.vcpu >= 1 && var.vcpu <= 64
    error_message = "vCPU count must be an integer from 1 to 64."
  }
}

variable "memory_mib" {
  description = "Dedicated RAM per VM in MiB."
  type        = number
  default     = 512
  validation {
    condition     = floor(var.memory_mib) == var.memory_mib && var.memory_mib >= 128
    error_message = "Memory must be an integer of at least 128 MiB."
  }
}

variable "disk_gib" {
  description = "Blank disk size in GiB; no image, ISO, clone, or installed OS."
  type        = number
  default     = 4
  validation {
    condition     = floor(var.disk_gib) == var.disk_gib && var.disk_gib >= 1
    error_message = "Disk size must be a positive integer in GiB."
  }
}

variable "datastore_id" {
  description = "Existing image-capable storage for blank disks; storage itself is never managed."
  type        = string
  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9_-]*$", var.datastore_id))
    error_message = "Specify an existing datastore identifier."
  }
}

variable "network_bridge" {
  description = "Existing isolated lab bridge; no bridge or host-network changes are made."
  type        = string
  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_.-]*$", var.network_bridge))
    error_message = "Specify an existing bridge name."
  }
}

variable "network_boot" {
  description = "Use network-first SeaBIOS/virtio PXE boot, then the blank disk."
  type        = bool
  default     = true
}

variable "started" {
  description = "Start the disposable guest. False avoids sending PXE requests until explicitly enabled."
  type        = bool
  default     = false
}

variable "ownership_tag" {
  description = "Required ownership marker; cannot be changed away from labfleet."
  type        = string
  default     = "labfleet"
  validation {
    condition     = var.ownership_tag == "labfleet"
    error_message = "The labfleet ownership tag is mandatory and cannot be replaced."
  }
}

variable "additional_tags" {
  description = "Optional tags in addition to the mandatory labfleet tag."
  type        = set(string)
  default     = []
  validation {
    condition     = alltrue([for tag in var.additional_tags : can(regex("^[a-z0-9][a-z0-9_-]*$", tag))])
    error_message = "Tags must use lowercase alphanumeric, underscore, or hyphen characters."
  }
}
