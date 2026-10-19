# Fleeting plugin for Proxmox Virtual Environment

This is a [fleeting](https://gitlab.com/gitlab-org/fleeting/fleeting) plugin for [Proxmox Virtual Environment](https://www.proxmox.com/en/proxmox-virtual-environment/overview). It autoscales GitLab Runner executors by cloning a template VM into a dedicated Proxmox pool.

This is a fork of [LukasK32/fleeting-plugin-proxmox](https://github.com/LukasK32/fleeting-plugin-proxmox), upgraded to a current `go-proxmox` client, switched to API-token authentication, and extended with per-instance network/VLAN configuration.

## Installation

See [Releases](https://github.com/myplixa/pve-fleeting-plugin/releases) for available versions and installation instructions.

## Configuration

### Runner configuration example

```toml
concurrent = 20
check_interval = 3

[[runners]]
  name = "ci-vm-small"
  url = "https://gitlab.example.com/"
  token = "glrt-XXXXXXXXXXXXXXXXXXXX"
  executor = "docker-autoscaler"
  limit = 10
  tags = ["ci-vm-small"]
  run_untagged = false

  [runners.docker]
    image = "example.registry/ci-default:latest"
    privileged = false
    volumes = ["/var/run/docker.sock:/var/run/docker.sock", "/cache"]

  [runners.autoscaler]
    plugin = "fleeting-plugin-proxmox"
    capacity_per_instance = 2
    max_use_count = 10
    max_instances = 5

    [runners.autoscaler.connector_config]
      username = "fleeting"
      use_external_addr = false

    [[runners.autoscaler.policy]]
      idle_count = 1
      idle_time = "5m0s"
      preemptive_mode = false

    [runners.autoscaler.plugin_config]
      name = "ci-vm-small"
      url = "https://pve.example.com:8006"
      credentials_file_path = "/etc/gitlab-runner/pve-credentials.json"
      pool = "gitlab-runners"
      storage = "local-zfs"
      template_id = 999
      instance_num_cpus = 2
      instance_memory_gb = 2
      [runners.autoscaler.plugin_config.instance_network]
        bridge = "vmbr0"
        vlan_tag = 100
        dhcp = true
        interface = "eth0"
      [[runners.autoscaler.plugin_config.instance_disks]]
        disk = "virtio0"
        size_gb = 20
```

See [Parameter Reference](#parameter-reference) for the `[[runners]]`/`[runners.autoscaler]` block (shared across all fleeting plugins) and [Plugin settings](#plugin-settings) for the complete `plugin_config` list below.

### Parameter Reference

| Section | Parameter | Description |
|---|---|---|
| top-level | `concurrent` | Total job slots across *all* `[[runners]]` sections on this control host combined |
| top-level | `check_interval` | How often (seconds) the runner manager polls GitLab for new jobs |
| `[[runners]]` | `name` | Display name for this runner registration in the GitLab UI |
| `[[runners]]` | `url` / `token` | GitLab instance URL and the runner authentication token, created via *Settings → CI/CD → Runners → New runner* |
| `[[runners]]` | `executor` | Must be `"docker-autoscaler"` to use Fleeting-managed instances with Docker |
| `[[runners]]` | `limit` | Max jobs *this runner section* will pull from GitLab at once. Should be ≥ `capacity_per_instance × max_instances`, or it becomes the real ceiling regardless of what the autoscaler could otherwise provide |
| `[[runners]]` | `tags` / `run_untagged` | Which jobs get routed to this runner. With multiple tiers (e.g. small/large), set `run_untagged = false` on each so a job without a matching tag doesn't land on an arbitrary tier |
| `[runners.docker]` | `image` | Default job container image if `.gitlab-ci.yml` doesn't specify one |
| `[runners.docker]` | `volumes` | Bind mounts into every job container. `/var/run/docker.sock:/var/run/docker.sock` is only needed if jobs themselves run `docker build`/`docker run` (Docker-in-Docker via the host socket) |
| `[runners.autoscaler]` | `plugin` | The Fleeting plugin binary name — `fleeting-plugin-proxmox` |
| `[runners.autoscaler]` | `capacity_per_instance` | Concurrent jobs allowed *on one VM* — they share that VM's CPU/RAM/Docker daemon, no isolation between them |
| `[runners.autoscaler]` | `max_use_count` | Total jobs a VM may serve over its lifetime before being replaced |
| `[runners.autoscaler]` | `max_instances` | Max VMs this runner section may have running at once. This is the **only** place to set it — the plugin itself has no `max_instances` setting; it reports a generous fixed ceiling (50) to GitLab Runner via `ProviderInfo.MaxSize`, and this value is what actually governs scaling |
| `[runners.autoscaler.connector_config]` | `username` | OS user the plugin connects as over SSH — must exist on the template/guest |
| `[runners.autoscaler.connector_config]` | `use_external_addr` | Whether to connect over the VM's external/public address instead of its internal one. `false` for an on-prem Proxmox host with no public IPs |
| `[[runners.autoscaler.policy]]` | `idle_count` | How many *idle* (no job running) VMs to keep on standby for instant job pickup. `0` = pure scale-to-zero |
| `[[runners.autoscaler.policy]]` | `idle_time` | How long idle VMs beyond active demand are kept before being torn down |
| `[[runners.autoscaler.policy]]` | `preemptive_mode` | Whether to provision instances ahead of confirmed demand |
| `[runners.autoscaler.plugin_config]` | *(all fields)* | This plugin's own settings — see [Plugin settings](#plugin-settings) below |

### Plugin settings

| Parameter                    | Type                      | Default value                      | Description                                                                                  |
| ----------------------------- | ------------------------- | ----------------------------------- | ---------------------------------------------------------------------------------------------- |
| `name`                        | string                    | N/A (required)                      | Identifies this instance group. Used as the prefix for generated VM names and, combined with `pool`, to tell this instance group's own VMs apart from others — including other instance groups sharing the same pool. See [VM Naming](#vm-naming). |
| `url`                         | string                    | N/A (required)                      | Proxmox VE URL (e.g. `https://pve.example.com:8006`).                                        |
| `insecure_skip_tls_verify`    | bool                      | `false`                             | If `true` then TLS certificate verification is disabled.                                     |
| `credentials_file_path`       | string                    | N/A (required)                      | Path to Proxmox VE API token credentials file.                                               |
| `pool`                        | string                    | N/A (required)                      | Name of the Proxmox VE pool to use.                                                           |
| `storage`                     | string                    | N/A (required if template is a VM, and no `instance_disks` entry sets its own `storage`)  | Name of the Proxmox VE storage used for the clone itself, and the default storage for any `instance_disks` entry that doesn't set its own `storage`. |
| `template_id`                 | int                       | N/A (required)                      | ID of the Proxmox VE VM to create instances from.                                             |
| `instance_network.interface`  | string                    | `ens18`                             | Network interface to read instance's IPv4 address from. See [note](#interface-naming) below. |
| `instance_network.protocol`   | `any` or `ipv4` or `ipv6` | `ipv4`                              | Network protocol to look for when discovering instance's IP address. `any` prioritizes IPv6. |
| `instance_network.bridge`     | string                    | *(unset — keep template's bridge)*  | Bridge to attach the deployed instance's `net0` device to.                                   |
| `instance_network.vlan_tag`   | int                       | *(unset — keep template's tag)*     | VLAN tag to set on `net0`. `0` explicitly removes a tag (plain, untagged bridge access).      |
| `instance_network.dhcp`       | bool                      | `false`                             | If `true`, configure cloud-init (`ipconfig0=ip=dhcp,ip6=dhcp`) so the instance actually requests a DHCP lease on boot. See [note](#cloud-init-networking) below. |
| `instance_num_cpus`           | int                       | *(unset — inherit template's value)* | Overrides the cloned VM's vCPU core count (`cores`). The template's socket count is left untouched. |
| `instance_memory_gb`          | int                       | *(unset — inherit template's value)* | Overrides the cloned VM's memory size, in GB.                                                |
| `instance_disks`              | array of `{disk, size_gb, storage}` | *(unset — inherit template's disks)* | Per-disk overrides. `disk` is a Proxmox disk key (`virtio0`, `scsi1`, `sata0`, ...). If that key already exists on the clone (inherited from the template), it's resized to `size_gb` — Proxmox does not support shrinking, so a smaller value is rejected by the API at deploy time. If the key doesn't exist yet, a brand-new disk of that size is allocated on this entry's own `storage` (falling back to the top-level `storage` if unset). See [Resource Sizing](#resource-sizing). |
| `instance_name_creating`      | string                    | `<name>-creating`                   | Base name for instances during creation. Each instance gets a unique `<base>-<shortUUID>` name — see [VM Naming](#vm-naming). |
| `instance_name_running`       | string                    | `<name>-running`                    | Base name for running instances.                                                              |
| `instance_name_removing`      | string                    | `<name>-removing`                   | Base name for instances during removal.                                                       |

`insecure_skip_tls_verify` disables TLS certificate verification entirely, leaving every API call to Proxmox vulnerable to interception/tampering. Prefer installing the Proxmox host's CA certificate into the runner host's trust store instead — that keeps verification on while trusting a self-signed/internal CA. The plugin logs a `WARN` on startup whenever `insecure_skip_tls_verify` is enabled.

### VM Naming

Every deployed instance gets a unique name: `<base>-<shortUUID>` (e.g. `ci-vm-small-running-a1b2c3d4`), where `<base>` is whichever of `instance_name_creating`/`_running`/`_removing` matches its current lifecycle state. The UUID suffix is generated once at clone time and preserved across every rename (`creating` → `running`, or → `removing`), so one instance keeps the same identifying suffix for its whole life — the same convention the vSphere/ESXi fleeting plugin uses, just applied on top of this plugin's rename-based state tracking (lifecycle state is read back from whichever base name prefix currently matches, not from an exact string).

GitLab Runner identifies instances by this generated name — `Update`/`ConnectInfo`/`Decrease` all use it as the instance ID, which is also what shows up in job logs (e.g. `Dialing instance ci-vm-small-running-a1b2c3d4...`) instead of an opaque VMID. Because the name's lifecycle-state prefix changes on rename (`creating` → `running` → `removing`), the ID GitLab Runner tracks for a given VM does too — this is safe because those renames only ever happen outside of the window GitLab Runner has a job tied to that ID (before a job is assigned, and after `Decrease` has already been called for it).

Because state matching is a prefix check against `instance_name_*`, and those default to `<name>-creating`/`<name>-running`/`<name>-removing`, **multiple `[[runners]]` sections with different `name` values can safely share one pool** — each instance group's collector only ever matches names carrying its own prefix, so it won't pick up (or tear down) another tier's VMs.

### Resource Sizing

`instance_num_cpus`, `instance_memory_gb` and `instance_disks` let multiple `[[runners]]` sections share a single template while requesting different hardware per tag/tier, instead of maintaining one template per size:

```toml
[[runners]]
  name = "ci-vm-small"
  # ...
  [runners.autoscaler.plugin_config]
    name = "ci-vm-small"
    template_id = 999
    instance_num_cpus = 2
    instance_memory_gb = 2
    [[runners.autoscaler.plugin_config.instance_disks]]
      disk = "virtio0"
      size_gb = 10

[[runners]]
  name = "ci-vm-large"
  # ...
  [runners.autoscaler.plugin_config]
    name = "ci-vm-large"
    template_id = 999
    instance_num_cpus = 8
    instance_memory_gb = 16
    [[runners.autoscaler.plugin_config.instance_disks]]
      disk = "virtio0"
      size_gb = 50
```

A second disk that doesn't exist on the template yet (e.g. `virtio1`) is allocated fresh rather than resized — useful for a separate data/cache disk per tier. Each entry falls back to the top-level `storage` when it doesn't set its own, so a disk only needs `storage` spelled out when it should land on a *different* storage pool than the rest of the instance:

```toml
    [[runners.autoscaler.plugin_config.instance_disks]]
      disk = "virtio0"
      size_gb = 20
    [[runners.autoscaler.plugin_config.instance_disks]]
      disk = "virtio1"
      size_gb = 100
      storage = "fast-nvme"
```

### Credentials file

Authentication is **API-token only** — there is no username/password/session-ticket path. Create a dedicated Proxmox user and a token for it (see [Proxmox configuration](#proxmox-configuration)), then point `credentials_file_path` at a JSON file:

```json
{
    "token_id": "fleeting@pve!fleeting",
    "token_secret": "<token-secret-shown-once-at-creation>"
}
```

| Parameter      | Type   | Description                                                   |
| -------------- | ------ | --------------------------------------------------------------- |
| `token_id`     | string | Full token ID, in the form `user@realm!tokenname`.             |
| `token_secret` | string | The token secret, shown once when the token is created.         |

This token can create/delete/reconfigure VMs across the whole Proxmox cluster, so `chmod 600` the credentials file — the plugin logs a warning on startup if it finds the file readable by group or other, but it will not refuse to start.

### Template VM configuration

The template must be a bootable VM with QEMU guest agent installed and running (see [Proxmox documentation](https://pve.proxmox.com/wiki/Qemu-guest-agent)).

#### Cloud-init networking

If the template doesn't already bring up networking on its own (e.g. a bare cloud-init template with no `netplan`/network-config baked in and no `ipconfig0` set), set `instance_network.dhcp = true` so the plugin configures `ipconfig0=ip=dhcp,ip6=dhcp` on the clone before starting it. Without this, a bare cloud-init image can boot with its NIC completely unconfigured — up, but with no IP at all.

#### Interface naming

Once cloud-init actually manages the interface (via `instance_network.dhcp` or a template that already does), Debian/Ubuntu cloud images commonly name it `eth0` rather than the kernel's predictable `ensNN` name — set `instance_network.interface` accordingly if `ConnectInfo` can't find an address.

### Proxmox configuration

You **MUST** create a **DEDICATED** user, pool, storage and API token for usage with this plugin. Any other configuration is untested and unsupported.

After creating a **DEDICATED** user, pool and storage, follow the procedure below:

1. Add the template VM as a member of the pool.
2. Add the following role for the user on the pool, with propagation enabled:
   * `PVEVMAdmin`.
3. Add the following role for the user on the storage:
   * `PVEDatastoreUser`.
4. Add the following role for the user, without propagation, on the node:
   * `PVEAuditor`.
5. **Proxmox VE 8.1+ / 9.x:** every bridge (even a plain, non-SDN `vmbr*` configured in `/etc/network/interfaces`) is exposed through an auto-created `localnetwork` SDN zone, and `VM.Clone` on a VM whose `net0` references that bridge is gated by **`SDN.Use`** on `/sdn/zones/localnetwork` — without it, cloning fails with `Permission check failed (/sdn/zones/<zone>/<bridge>, SDN.Use)`. Add the following role for the user on `/sdn/zones/localnetwork` (or the specific zone your bridges live in):
   * `PVESDNUser`.
6. Create an API token for the user (e.g. `pveum user token add <user>@pve <tokenname> --privsep 0`) and use it as described in [Credentials file](#credentials-file).

```shell
pveum user add fleeting@pve --comment "fleeting-plugin-proxmox automation"
pveum acl modify /pool/<pool> -user fleeting@pve -role PVEVMAdmin
pveum acl modify /storage/<storage> -user fleeting@pve -role PVEDatastoreUser
pveum acl modify /nodes/<node> -user fleeting@pve -role PVEAuditor -propagate 0
pveum acl modify /sdn/zones/localnetwork -user fleeting@pve -role PVESDNUser
pvesh set /pools/<pool> -vms <template_id>
pveum user token add fleeting@pve fleeting --privsep 0
```

## Development

### Unit tests

```shell
make unit-test
```

### Integration tests

1. Create and fill out `credentials.json` file in project root (see [Credentials file](#credentials-file) for details)
    ```bash
    cp credentials.example.json credentials.json
    ```
2. Create and fill out `config.json` file in project root (see [Plugin settings](#plugin-settings) for details)
    ```bash
    cp config.example.json config.json
    ```
3. Run integration tests:
    ```bash
    make integration-test
    ```
