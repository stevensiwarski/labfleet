# Isolated PXE provisioning

`provisionctl` renders and checks a one-target dnsmasq/iPXE/HTTP/Ubuntu-autoinstall
configuration. Its runtime is intended only for a positively identified
LabFleet-owned provisioning host with a dedicated isolated NIC, never either
bootstrap/control system.

```sh
go build -o bin/provisionctl ./provisioning/cmd/provisionctl
go test ./provisioning/...
```

Run these build commands from the repository root. Start with
`config.example.json`; the documentation-only addresses, MACs, and public-key
placeholder must be replaced in **ignored local configuration**, not committed.
`labfleet-provisioning.service` is an optional deployment unit for the owned
service host; it is not installed automatically.

See [the provisioning guide](../docs/provisioning.md) for verified artifact
handling, deployment prerequisites, runtime safety checks, VM identity, and the
live acceptance procedure. Live provisioning is currently blocked by the absence
of an identified isolated provisioning network and owned service host. No
successful PXE/autoinstall/SSH or repeatability result is claimed yet.
