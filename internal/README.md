# Internal packages

Private reusable Go packages shared by LabFleet commands belong here.

`tofusafety` checks saved OpenTofu VM plans against live Proxmox ownership using
read-only API calls. Tests use local mock servers, not lab infrastructure.
