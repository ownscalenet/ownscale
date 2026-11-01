---
title: Machines
weight: 1
---

A machine (Tailscale calls it a *node*) is a device or container running the
Tailscale client and registered in your organization. The **Machines** page
lists them all.

## The machine list

Each row shows the machine's name, owner, tailnet addresses, tags and when it
was last seen. Filter by name, by owner or by status: online, offline or
expired. Click a machine to open its detail page, which shows:

- all its addresses (IPv4, IPv6 and MagicDNS name);
- **Subnets and routing**: its approved routes, the routes awaiting approval
  and whether it is an exit node;
- an **Update available** badge when its Tailscale client is out of date.

## Adding a machine

Click **Register machine** to see the command for your organization:

```sh
tailscale up --login-server=https://<org>.<BASE_DOMAIN>
```

Then either open the URL that `tailscale up` prints and sign in, or paste
that URL or registration key into the **Register machine** dialog and choose
the member who will own the machine. Managers can register machines for any
member, and members only for themselves. To register without a browser, use
an [auth key]({{< relref "auth-keys.md" >}}). See
[Connect a client]({{< relref "/docs/get-started/clients.md" >}}).

## Machine actions

Each machine has a menu with these actions:

| Action | What it does | Who can do it |
| --- | --- | --- |
| Rename | Changes the machine's name, which is also its MagicDNS name. | owner of the machine, managers |
| Tags | Sets the machine's [tags]({{< relref "tags.md" >}}). | managers |
| Routes | Approves or removes advertised [subnet routes and exit node]({{< relref "subnet-routers-and-exit-nodes.md" >}}). | managers |
| Expiry | Expires the node key now, sets an expiry date, or disables expiry. | owner of the machine, managers |
| Remove | Deletes the machine from the tailnet. It must be registered again to rejoin. | owner of the machine, managers |
| SSH | Opens a [browser SSH session]({{< relref "tailscale-ssh.md#ssh-from-the-browser" >}}). | anyone allowed by the policy's `ssh` rules |

**Expiring** a machine signs it out: it stays in the list but can't connect
until someone signs in on it again. **Disabling expiry** is useful for
servers you don't want to re-authenticate periodically.

## Internal machines

Machines marked **Internal** are created by ownscale itself. Today the only
one is the `ownscale-agent` machine that forwards
[funnel]({{< relref "funnels.md" >}}) traffic. Removing it also deletes all
of the organization's funnel routes. See the
[FAQ]({{< relref "/docs/faqs.md#ownscale-agent-user--machine" >}}).

## Addresses

Machines get addresses from `100.64.0.0/10` and `fd7a:115c:a1e0::/48`,
assigned in order. A machine keeps its address for as long as it stays
registered.
