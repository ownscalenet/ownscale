---
title: osa CLI
weight: 10
---

`osa` is the operator command-line tool shipped in the `ownscale` image. It
reads the same environment variables as the server.

Run it in the running container:

```sh
make osa <command>
```

## organization

| Command | Description |
| --- | --- |
| `make osa organization list` | List organizations. |
| `make osa organization delete <subdomain>...` | Delete organizations with their machines, data and backups. Needs the running server. |

## user

| Command | Description |
| --- | --- |
| `make osa user list` | List users and their organizations. |
| `make osa user delete <user_id>` | Delete a user, their Headscale user and all their machines. Needs the running server. |

## headscale

```sh
make osa headscale <subdomain> [headscale args...]
```

Runs the Headscale CLI against one organization's instance, through its unix
socket. It needs the running server.
For example:

```sh
make osa headscale aaaa nodes list
```

Prefer the console for routine changes. This command is for inspection and
emergencies.

## migrate

| Command | Description |
| --- | --- |
| `make osa migrate up` | Apply the next pending migration. Prints `no pending migrations` when done. |
| `make osa migrate down` | Revert the last applied migration. |
| `make osa migrate status` | Show the status of every migration. |

The container's entrypoint applies all pending migrations on every start,
so you rarely need these.

## verify

| Command | Description |
| --- | --- |
| `make osa verify db` | Fail if ownscale's database is missing but has a backup. |
| `make osa verify organizations` | The same for every organization's database. |

Both run automatically on container start.

## restore

| Command | Description |
| --- | --- |
| `make osa restore db` | Restore ownscale's database from backup, if missing locally. |
| `make osa restore organizations` | Restore every organization's database from backup, if missing locally. |

Run `restore db` first. Backups are configured with the
`OWNSCALE_BACKUP_*` settings in `example.env`.

## Development commands

| Command | Description |
| --- | --- |
| `make osa gen-local-ca` | Generate the local development root CA. Does nothing if it exists. |
| `make osa devbox-key <subdomain>` | Print a short-lived, ephemeral auth key for an organization. Used by `make devbox`. |
