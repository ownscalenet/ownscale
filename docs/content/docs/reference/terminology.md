---
title: Terminology
weight: 30
---

Terms used in ownscale's docs and console. Most come from Tailscale; the
ownscale-specific ones note how ownscale differs. Tailscale's own
[glossary](https://tailscale.com/docs/reference/glossary) covers more.

### Access control policy

The HuJSON document that decides which users and devices can reach each
other, and who can SSH where. It is edited under
[Access control](/console/access-control) in the console.
[Tailscale docs](https://tailscale.com/docs/features/access-control/acls)

### Auth key

Also called a pre-auth key. A key that registers a device without an
interactive login, which is useful for servers and containers. Keys can be
reusable, ephemeral, tagged, or any combination of these.
[Tailscale docs](https://tailscale.com/docs/features/access-control/auth-keys)

### Authentication restrictions

An organization-wide allowlist of email domains, groups and users that may
sign in to the organization through OIDC.

### Autogroup

A built-in selector in the access control policy, such as `autogroup:member`
or `autogroup:self`, that matches a set of users or devices without listing
them.
[Tailscale docs](https://tailscale.com/docs/reference/targets-and-selectors)

### Console

ownscale's web interface at `https://console.<BASE_DOMAIN>`, where
organizations, users, devices, keys and policies are managed.
[Open the console](/console/)

### Content domain

An optional second domain (`CONTENT_DOMAIN`) that serves funnels and
devices' MagicDNS names, away from the console.
[More](/docs/self-hosting/content-domain/)

### Coordination server

The server that Tailscale clients register with to exchange keys, policies
and network maps. It's also called the control plane. ownscale runs one
Headscale coordination server per organization, and your traffic never passes
through it.
[Tailscale docs](https://tailscale.com/docs/concepts/control-data-planes)

### DERP

Relay servers that forward encrypted traffic when two devices can't connect
directly. Each organization chooses its own on the DNS page.
[Tailscale docs](https://tailscale.com/docs/reference/derp-servers)

### Device

A computer, phone, server or process that has joined the tailnet. Tailscale
also calls it a machine, and Tailscale and Headscale call it a node.

### Ephemeral device

A device registered with an ephemeral auth key. It is deleted automatically
once it logs out or has been offline for 120 seconds, which suits CI jobs and
short-lived containers.
[Tailscale docs](https://tailscale.com/docs/features/ephemeral-nodes)

### Exit node

A device that other devices can send all their internet traffic through.
The exit node has to be approved before others can use it.
[Tailscale docs](https://tailscale.com/docs/features/exit-nodes)

### Funnel

ownscale's way of exposing a service in your tailnet to the public internet
at `https://<label>-<org>.<CONTENT_DOMAIN>`. It replaces `tailscale funnel`,
which doesn't work with ownscale. See
[Limitations]({{< relref "limitations.md" >}}).

### Grants

The newer access control syntax that combines network access and
application capabilities in one rule. Headscale doesn't support every grant
feature yet.
[Tailscale docs](https://tailscale.com/docs/features/access-control/grants)

### Headscale

The open-source implementation of Tailscale's coordination server that
ownscale is built on. Its CLI is available through
[`osa headscale`]({{< relref "osa-cli.md#headscale" >}}).
[Headscale docs](https://headscale.net)

### Invitation

A link that lets someone join an organization with a given role. ownscale
doesn't email invitations, so share the link yourself.

### Key expiry

How long a device's node key stays valid before the device has to log in
again. It can be disabled per device, and tagged devices never expire.
[Tailscale docs](https://tailscale.com/docs/features/access-control/key-expiry)

### Login server

The organization's coordination server URL, `https://<org>.<BASE_DOMAIN>`.
You pass it to `tailscale up --login-server` or set it in the client's
settings. See [Connecting devices]({{< relref "/docs/connect" >}}).

### MagicDNS

Automatic DNS names for every device, such as
`laptop.<tailnet label>.<CONTENT_DOMAIN>`, which resolve only inside the
tailnet.
[Tailscale docs](https://tailscale.com/docs/features/magicdns)

### Nameservers and split DNS

The DNS servers that devices in the tailnet use. Split DNS sends queries for
specific domains to specific nameservers.
[Tailscale docs](https://tailscale.com/docs/reference/dns-in-tailscale)

### Organization

ownscale's unit of tenancy: one tailnet, with its own Headscale instance,
login server, users and policy. An account belongs to one organization.

### osa

The operator CLI that ships with ownscale, for managing organizations,
backups and migrations on the server. See
[osa CLI]({{< relref "osa-cli.md" >}}).

### Role

What a member of an organization may do: `owner`, `admin`, `network_admin`,
`it_admin`, `auditor` or `member`. The last four currently have the same
permissions.

### Subnet router

A device that advertises routes to a network that can't run Tailscale client
itself, such as a home LAN. Advertised routes must be approved before other
devices use them.
[Tailscale docs](https://tailscale.com/docs/features/subnet-routers)

### Tags

Labels such as `tag:server` that give a device an identity separate from the
user who added it. Policies can target tags, and tagged devices don't expire.
[Tailscale docs](https://tailscale.com/docs/features/tags)

### Taildrop

Sending files directly between your devices. It's always on in ownscale.
[Tailscale docs](https://tailscale.com/docs/features/taildrop)

### Tailnet

The private network formed by an organization's devices.
[Tailscale docs](https://tailscale.com/docs/concepts/tailnet)

### Tailnet label

The random label, such as `net3k9x2a`, that forms the tailnet name, which is
the DNS suffix of every MagicDNS name. It's chosen when the organization is
created and can't be changed.

### Tailscale client

The official Tailscale apps for Linux, macOS, Windows, iOS and Android. They
work with ownscale once they're pointed at your login server.

### Tailscale SSH

SSH access between devices, authorized by the `ssh` section of the access
control policy instead of SSH keys.
[Tailscale docs](https://tailscale.com/docs/features/tailscale-ssh)

### WireGuard

The VPN protocol Tailscale uses to encrypt traffic between devices.
[Tailscale docs](https://tailscale.com/docs/concepts/wireguard)
