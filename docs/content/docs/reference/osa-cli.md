---
title: osa CLI
weight: 2
---

`osa` is the operator command-line tool shipped in the `ownscale` image. It
reads the same environment variables as the server.

Run it in the running container:

```sh
docker compose exec ownscale osa <command>
```

Commands that must run while ownscale is **stopped**, namely `restore` and
`migrate down`, use a one-off container instead:

```sh
docker compose run --rm --entrypoint osa ownscale <command>
```

From a source checkout, `make osa <command>` is a shortcut for the first form.

## organization

| Command | Description |
| --- | --- |
| `osa organization list` | List organizations. |

## user

| Command | Description |
| --- | --- |
| `osa user list` | List users and their organizations. |
| `osa user delete <user_id>` | Delete a user, their Headscale user and all their machines. Needs the running server. |

## headscale

```sh
osa headscale <subdomain> [headscale args...]
```

Runs the Headscale CLI against one organization's instance, through its unix
socket. It needs the running server, and the organization must be awake.
For example:

```sh
docker compose exec ownscale osa headscale abcd nodes list
docker compose exec ownscale osa headscale abcd policy get
```

Prefer the console for routine changes. This command is for inspection and
emergencies.

## migrate

| Command | Description |
| --- | --- |
| `osa migrate up` | Apply the next pending migration. Prints `no pending migrations` when done. |
| `osa migrate down` | Revert the last applied migration. |
| `osa migrate status` | Show the status of every migration. |

The container's entrypoint applies all pending migrations on every start,
so you rarely need these.

## verify

| Command | Description |
| --- | --- |
| `osa verify db` | Fail if ownscale's database is missing but has a backup. |
| `osa verify organizations` | The same for every organization's database. |

Both run automatically on container start.

## restore

| Command | Description |
| --- | --- |
| `osa restore db` | Restore ownscale's database from backup, if missing locally. |
| `osa restore organizations` | Restore every organization's database from backup, if missing locally. |

Run `restore db` first. Backups are configured with the
`OWNSCALE_BACKUP_*` settings in `example.env`.

## Development commands

| Command | Description |
| --- | --- |
| `osa gen-local-ca` | Generate the local development root CA. Does nothing if it exists. |
| `osa devbox-key <subdomain>` | Print a short-lived, ephemeral auth key for an organization. Used by `make devbox`. |
