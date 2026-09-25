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
live acceptance results and procedure.

The [persistent infrastructure root](../infra/opentofu/provisioner/README.md)
automates the isolated Simple SDN zone/VNet and dedicated provisioner VM through
the Proxmox API. `seedctl` renders its cloud-init bootstrap:

```sh
go build -o bin/seedctl ./provisioning/cmd/seedctl
bin/seedctl --config /private/path/bootstrap.local.json
```

`bootstrap.example.json` is a documentation-only shape reference. Choose unused
private lab addressing, matching new-VM NIC MACs, an external public key, and a
fresh output path inside a private directory. The seed files become a small
NoCloud ISO for the provisioner; the disposable target has no local install media.
