---
title: Quickstart
weight: 1
---

This page takes you from an empty account to two connected machines. It
assumes an ownscale instance is already running, either the demo at
[console.ownscale.net](https://console.ownscale.net) or
[your own]({{< relref "/docs/self-hosting/installation.md" >}}). In the
examples, the instance's base domain is `example.com`.

{{% steps %}}

### Sign in

Open `https://console.example.com/login` and sign in with one of the
providers shown, or create an account with a passkey on the sign-up page.

The first time you sign in without an invite, ownscale creates a new
[organization]({{< relref "/docs/concepts/organizations.md" >}}) for you and
makes you its **owner**. The organization is your private network, your
*tailnet*.

### Find your login server

The console header shows your organization's **login server**, for example
`https://abcd.example.com`. Tailscale clients use this URL instead of
Tailscale's own servers. Copy it with the copy button next to it.

### Install Tailscale on a machine

Install the standard Tailscale client from
[tailscale.com/download](https://tailscale.com/download). ownscale works with
the official, unmodified clients. See
[Connect clients]({{< relref "clients.md" >}}) for per-platform notes.

### Connect the machine

On the machine, run:

```sh
sudo tailscale up --login-server=https://abcd.example.com
```

The command prints a URL. Open it and sign in with the same account you used
for the console. The machine appears on the **Machines** page of the console
with its tailnet IP address, such as `100.64.0.1`.

### Connect a second machine

Repeat the last two steps on another device. Both machines can now reach
each other by IP address, or by name if
[MagicDNS]({{< relref "/docs/features/dns.md" >}}) is on:

```sh
ping 100.64.0.2
tailscale ping my-laptop
```

### Invite a teammate (optional)

On **Users**, create an invite and send the link to your teammate. When they
accept it, they join your organization instead of getting a new one. See
[Invites]({{< relref "/docs/features/invites.md" >}}).

{{% /steps %}}

## Next steps

- Lock down who can reach what with
  [access control]({{< relref "/docs/features/access-control.md" >}}).
- Reach a whole network through one machine with a
  [subnet router]({{< relref "/docs/features/subnet-routers-and-exit-nodes.md" >}}).
- Route all your traffic through a machine with an
  [exit node]({{< relref "/docs/features/subnet-routers-and-exit-nodes.md" >}}).
- Learn the vocabulary in [Terminology]({{< relref "/docs/concepts/terminology.md" >}}).
