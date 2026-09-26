# node-doctor operator guide

## Purpose and safety boundary

`node-doctor` performs bounded, read-only diagnostics for explicitly configured LabFleet cluster nodes. The controller-side CLI uses the same private JSON configuration loader as `fleetctl` (`fleetctl.ReadConfig`): configuration must be a private regular, non-symlink file (no group/other permissions), and references private vars/work directories. It resolves a target only from configured cluster membership, verifies ownership against the provider, and validates the generated inventory before connecting.

Remote access is pinned SSH: batch mode, strict host-key checking, a configured identity key, and the inventory's pinned `known_hosts` file and host-key alias. It sends a structured JSON request on stdin to the fixed remote `probe` subcommand through `sudo -n`; it does not accept or execute arbitrary remote commands. The remote probe also checks the actual hostname against the requested node and refuses non-LabFleet/protected host identities (including provisioner and coding-agent names). No normal CLI invocation installs software or changes services.

Run this only against LabFleet-managed nodes, using the private configuration and artifacts produced by the configured discovery process. Never substitute a hand-copied or legacy inventory: the configured inventory must be exactly `<cluster_private_dir>/inventory.json` from discovery vars, and must carry the expected pinned host-key configuration. A copied inventory from the earlier Issue 13 workflow is not valid remote authorization. Keep config, discovery vars, inventory, SSH key, `known_hosts`, kubeconfig, and command output containing infrastructure details private; do not paste their contents or real addresses into public docs/issues.

## Build and install

Build from the repository root with the supported static-binary setting:

```sh
CGO_ENABLED=0 go build -o bin/node-doctor ./cmd/node-doctor
```

Install/deploy that same built binary only on explicitly owned LabFleet nodes, at the fixed root-owned path `/usr/local/bin/node-doctor` (mode `0755`). There is no executable-path override or arbitrary remote-command option. The CLI does not auto-install or copy binaries. Use the approved node-configuration process or an operator-controlled transfer with the existing pinned SSH identity; stage transfers in private mode-`0700` directories. The existing `fleet` account must be able to invoke this fixed diagnostic operation with non-interactive sudo. Do not broaden sudo rights as part of diagnostics.

## Commands

Set `CONFIG` below to the path of the private `fleetctl` JSON config; optionally set `SCOPE` to the configured cluster target name. These placeholders intentionally contain no environment-specific path or address.

```sh
node-doctor node NODE_NAME --config "$CONFIG" --scope "$SCOPE"
node-doctor network NODE_NAME --config "$CONFIG" --scope "$SCOPE"
node-doctor check NODE_NAME --config "$CONFIG" --scope "$SCOPE"
node-doctor cluster --config "$CONFIG" --scope "$SCOPE"
```

`--scope` defaults to the config's `default_target` when present. Node names must match an explicitly configured, eligible node. `node` runs host and network checks plus cluster-wide Kubernetes checks and that node's Kubernetes conditions. `check` is an alias for `node`. `network` runs network checks only, and deliberately does not append Kubernetes checks. `cluster` takes no node name and runs the shared cluster checks.

Options:

| Option | Default | Bounds / meaning |
| --- | --- | --- |
| `--config PATH` | `$FLEETCTL_CONFIG` | Private fleetctl config; required if env var unset |
| `--scope TARGET` | Config default target | Must identify a configured cluster |
| `--output text|json` | `text` | Select human-readable table or JSON report |
| `--concurrency N` | `4` | Probe worker count, 1–8 |
| `--timeout-seconds N` | `10` | Per-check timeout, 1–60 seconds |
| `--overall-timeout-seconds N` | `120` | Whole operation timeout, 1–600 seconds |
| `--dns-name NAME` | `pkgs.k8s.io` | DNS name resolved from the node |
| `--tcp HOST:PORT` | none | Additional endpoint check; repeat up to 8 |

Timeouts bound diagnostics, not a promise that every external operation completes within precisely that time. Kubernetes inspection runs two bounded concurrent tasks; kubectl calls have 30-second command timeouts and the shared fleetctl inspection uses its established provider/API timeouts, all subject to the overall deadline. A check that ignores cancellation retires its worker; unfinished checks are reported as failed. The worker bound is per invocation: an uncooperative check goroutine may remain until its operation returns, so library callers must not repeatedly retry stuck runs. The CLI process exits after reporting, and subprocesses use runner process-group cancellation. Results are emitted in deterministic check order, not completion order. Durations are milliseconds.

## Checks and interpretation

The `node`/`check` mode reports host resource, lifecycle, and network findings, then reuses fleetctl's cluster-status inspection and reports additional per-node Kubernetes conditions. Thus a selected node's report can include a dependency failure elsewhere in the cluster. The network command is limited to the node-local network set. The cluster command does not SSH to individual hosts.

Host checks read Linux `/proc` and filesystem APIs: one-minute load normalized by CPU count; CPU and memory PSI `avg10`; available memory; block and inode use for `/` and `/var/lib/containerd`; time synchronization; `containerd` and `kubelet` active services; containerd socket reachability; and CRI plugin health from `ctr plugins ls`. Thresholds in the current implementation:

* CPU normalized one-minute load: WARN at 1, FAIL at 2.
* CPU PSI (`some`, `avg10`): WARN at 20, FAIL at 50.
* Memory PSI (`full`): WARN at 5, FAIL at 20.
* Available memory: WARN at or below 10%, FAIL at or below 5%.
* Disk block and inode use: WARN at 80%, FAIL at 95%.
* Missing PSI data is WARN; absent `/var/lib/containerd` is WARN. Other unavailable/invalid required measurements fail.

CPU load is a load-average signal, **not** CPU utilization. PSI thresholds reflect the kernel's reported `avg10` value. Networking checks verify active IPv4 on `mgmt0` (and match the configured management address), a default route through `mgmt0`, gateway ICMP response, DNS resolution latency (WARN above 500 ms, FAIL above 2 seconds), TCP connectivity to the configured Kubernetes API endpoint, and any requested additional TCP endpoints. Gateway reachability specifically uses ICMP ping: filtered/disabled ICMP can produce FAIL even when TCP connectivity works; conversely, a responding gateway does not prove general TCP reachability. DNS lookup and TCP checks originate on the node.

Kubernetes reporting reuses fleetctl's read-only cluster status checks (configured VM/provider identity and ownership, expected node topology/roles/readiness, and system-pod readiness including Cilium, its operators, and CoreDNS), plus expected-node conditions: `Ready`, `MemoryPressure`, `DiskPressure`, `PIDPressure`, and `NetworkUnavailable` (the last is optional when not published). Pod readiness is an availability signal, not deep application health. This is not an observability/metrics or chaos-testing framework.

No finding triggers a restart, repair, or other remediation. Use the report as diagnostic evidence; investigate ownership and cause before any separate corrective operation.

## Output and exit codes

Text output contains target, aggregate status, and rows with check, result, details/message, hint, and duration. JSON is one report object with `target`, aggregate `status`, `exit_code`, and ordered `results`. Each result includes `check`, `status`, `message`, `target`, `duration_ms`, and optional `details` and `hint`. Individual check status is `PASS`, `WARN`, or `FAIL`. Aggregate status is `healthy` (all pass), `degraded` (at least one warning and no failures), or `unhealthy` (failure or invalid/empty results).

| Exit | Meaning |
| ---: | --- |
| 0 | Healthy; all checks passed |
| 1 | Degraded; one or more warnings and no failures |
| 2 | Invalid command, options, configuration/request, or endpoint |
| 3 | Unhealthy diagnostic result or remote transport/diagnostic failure |
| 4 | Target is not an eligible configured LabFleet cluster/node, or identity/ownership could not be established |
| 130 | Interrupted or overall deadline canceled the run |

Consumers should use both the process exit status and the JSON `exit_code`; cancellation reports aggregate status `canceled`. Do not interpret exit 0 as proof beyond the checks listed above. Errors in output should be handled as potentially sensitive operational information.

## Live validation

The retained cluster was used without rebuilding VMs or changing networking.
The Go binary was installed at the fixed root-owned path on the two explicitly
attested test nodes; no probe was installed on the coding agent, persistent
provisioner, or Proxmox management instance. Normal diagnostic runs made no
service changes.

| Baseline | Observed result |
| --- | --- |
| `node labfleet-cp-01` | 33 PASS, exit 0; approximately 0.80 seconds |
| `node labfleet-worker-02` | 33 PASS, exit 0; approximately 0.81 seconds |
| `cluster` | 42 PASS, exit 0; approximately 0.47 seconds |
| Text and JSON modes | Passed for the control plane, worker, and cluster |

### One controlled worker failure

The selected node was **labfleet-worker-02 (VM 930085)**. Before any service
change, Proxmox evidence matched its configured identity, running state, and
`labfleet`, `disposable`, `issue8`, and `worker` tags. Non-interactive recovery
access was verified. All six nodes were Ready and all 33 pods were healthy
system pods; no application workloads were present.

1. A fresh node-doctor baseline returned 33 PASS and exit 0.
2. The operator test stopped only that worker's `containerd` service. A transient
   worker-local 90-second recovery timer was armed as a fallback, in addition
   to a controller-side `finally` restart.
3. Node-doctor returned **exit 3**, identifying **containerd service**, **containerd
   socket**, and **containerd CRI** as FAIL. Detection completed approximately
   **1.00 second** after initiating the stop; the diagnostic invocation itself
   took approximately **0.75 seconds**.
4. Kubernetes reported the worker's **Ready=False** condition approximately
   **32.45 seconds** after the stop. Diagnostics detected the runtime failure
   before Kubernetes readiness changed.
5. The operator restarted containerd and canceled the fallback timer.
6. Node-doctor returned **33 PASS, exit 0** again approximately **9.23 seconds**
   after recovery began. All **six nodes were Ready**, all **33 system pods were
   Running with every container Ready**, and Cilium/operator/CoreDNS checks passed.

This was one scoped diagnostic experiment, not a chaos-testing framework.
Timing is an observation from this run, not a latency guarantee. Raw reports
and access artifacts remain private.

## Tests and limitations

Unit fixtures cover aggregation/exit codes/JSON, thresholds and Linux parsers,
routes, local-only DNS/TCP success and failure, CRI plugin variants, Kubernetes
conditions and missing Ready, ownership refusals, SSH pinning, forbidden remote
executables, cancellation, timeout handling, bounded concurrency, and stable
result ordering. Existing fleetctl component fixtures cover Cilium/CoreDNS
degradation, and fleetctl, tofu-check, and runner regression tests remain intact.

Checks are Linux-specific and currently follow the reviewed fleet's `mgmt0`,
containerd paths, and fixed six-node configuration. Disk checks cover explicit
root/runtime data paths, not every mounted filesystem. TCP API connectivity
does not itself prove API authorization; controller Kubernetes inspection
provides the authenticated health view. Time synchronization uses the system's
reported synchronization state (`timedatectl`, with a chrony fallback), not an
independent NTP offset measurement. The remote binary must be installed before
node/network checks; cluster mode requires no remote binary. The internal
`probe` protocol is not an arbitrary-command or repair API.

Initial SSH setup uncovered a copied inventory lacking adjacent pinned-host
artifacts; the private configuration was corrected to use the original generated
inventory and its matching discovery inputs. Safety review removed a selectable remote executable
path and made cancellation report `status=canceled` rather than contradictory
healthy output. No credentials or private environment values belong in public
reports or committed examples.
