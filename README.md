# Fleeting plugin for Proxmox Virtual Environment

This is a [fleeting](https://gitlab.com/gitlab-org/fleeting/fleeting) plugin for [Proxmox Virtual Environment](https://www.proxmox.com/en/proxmox-virtual-environment/overview). It autoscales GitLab Runner executors by cloning a template VM into a dedicated Proxmox pool.

This is a fork of [LukasK32/fleeting-plugin-proxmox](https://github.com/LukasK32/fleeting-plugin-proxmox), upgraded to a current `go-proxmox` client, switched to API-token authentication, and extended with per-instance network/VLAN configuration.

## Installation

See [Releases](https://github.com/myplixa/pve-fleeting-plugin/releases) for available versions and installation instructions.

## Configuration

### Plugin settings

| Parameter                    | Type                      | Default value                      | Description                                                                                  |
| ----------------------------- | ------------------------- | ----------------------------------- | ---------------------------------------------------------------------------------------------- |
| `url`                         | string                    | N/A (required)                      | Proxmox VE URL (e.g. `https://pve.example.com:8006`).                                        |
| `insecure_skip_tls_verify`    | bool                      | `false`                             | If `true` then TLS certificate verification is disabled.                                     |
| `credentials_file_path`       | string                    | N/A (required)                      | Path to Proxmox VE API token credentials file.                                               |
| `pool`                        | string                    | N/A (required)                      | Name of the Proxmox VE pool to use.                                                           |
| `storage`                     | string                    | N/A (required if template is a VM)  | Name of the Proxmox VE storage to use.                                                        |
| `template_id`                 | int                       | N/A (required)                      | ID of the Proxmox VE VM to create instances from.                                             |
| `max_instances`               | int                       | N/A (required)                      | Maximum instances than can be deployed.                                                       |
| `instance_network_interface`  | string                    | `ens18`                             | Network interface to read instance's IPv4 address from. See [note](#interface-naming) below. |
| `instance_network_protocol`   | `any` or `ipv4` or `ipv6` | `ipv4`                              | Network protocol to look for when discovering instance's IP address. `any` prioritizes IPv6. |
| `instance_network_bridge`     | string                    | *(unset — keep template's bridge)*  | Bridge to attach the deployed instance's `net0` device to.                                   |
| `instance_network_vlan_tag`   | int                       | *(unset — keep template's tag)*     | VLAN tag to set on `net0`. `0` explicitly removes a tag (plain, untagged bridge access).      |
| `instance_network_dhcp`       | bool                      | `false`                             | If `true`, configure cloud-init (`ipconfig0=ip=dhcp,ip6=dhcp`) so the instance actually requests a DHCP lease on boot. See [note](#cloud-init-networking) below. |
| `instance_name_creating`      | string                    | `fleeting-creating`                 | Name to set for instances during creation.                                                    |
| `instance_name_running`       | string                    | `fleeting-running`                  | Name to set for running instances.                                                            |
| `instance_name_removing`      | string                    | `fleeting-removing`                 | Name to set for instances during removal.                                                     |

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

### Template VM configuration

The template must be a bootable VM with QEMU guest agent installed and running (see [Proxmox documentation](https://pve.proxmox.com/wiki/Qemu-guest-agent)).

#### Cloud-init networking

If the template doesn't already bring up networking on its own (e.g. a bare cloud-init template with no `netplan`/network-config baked in and no `ipconfig0` set), set `instance_network_dhcp: true` so the plugin configures `ipconfig0=ip=dhcp,ip6=dhcp` on the clone before starting it. Without this, a bare cloud-init image can boot with its NIC completely unconfigured — up, but with no IP at all.

#### Interface naming

Once cloud-init actually manages the interface (via `instance_network_dhcp` or a template that already does), Debian/Ubuntu cloud images commonly name it `eth0` rather than the kernel's predictable `ensNN` name — set `instance_network_interface` accordingly if `ConnectInfo` can't find an address.

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
