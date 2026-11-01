---
title: Tailscale SSH
weight: 16
---

Tailscale SSH lets users SSH into machines in the tailnet without managing
SSH keys. The Tailscale client on the target machine runs the SSH server, and
the policy's `ssh` rules decide who may log in as which local user.

## Requirements

1. The target machine runs the SSH server:
   ```sh
   sudo tailscale set --ssh
   ```
   Tailscale SSH is available on Linux and macOS (the open-source
   `tailscaled` variant). See Tailscale's
   [SSH docs](https://tailscale.com/kb/1193/tailscale-ssh).
2. The policy's `ssh` section allows the connection.
3. The policy's `acls` or `grants` allow port 22 from the source to the
   target.

## SSH rules

```jsonc
"ssh": [
  {
    "action": "accept",
    "src": ["autogroup:member"],
    "dst": ["autogroup:self"],
    "users": ["autogroup:nonroot", "root"],
  },
  {
    "action": "accept",
    "src": ["group:ops"],
    "dst": ["tag:server"],
    "users": ["ubuntu"],
  },
],
```

- `src` and `dst` can't be `*`. Use users, groups, tags, `autogroup:member`,
  `autogroup:tagged` or `autogroup:self`.
- `dst: ["autogroup:self"]` means the source user's own machines, and needs
  users or groups as `src`.
- `users` lists the local accounts allowed on the target.
  `autogroup:nonroot` means any account except `root`.

New organizations start with the first rule above. See
[Access control]({{< relref "access-control.md" >}}).

## Connecting from a machine

From any machine in the tailnet:

```sh
ssh ubuntu@my-server
# or
tailscale ssh ubuntu@my-server
```

## SSH from the browser

Choose **SSH** from a machine's menu on **Machines** to open a terminal in
the browser. Enter the username on the target and connect.

The browser joins the tailnet as a temporary, ephemeral machine owned by
you, using a single-use key that ownscale creates for the session. The same
`ssh` rules apply as for any other machine you own. The target must be
online, not expired, and running `tailscale set --ssh`.

See Tailscale's [SSH docs](https://tailscale.com/kb/1193/tailscale-ssh).
