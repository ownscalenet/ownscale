---
title: FAQs
weight: 7
---

## ownscale-agent user & machine

When creating a funnel, ownscale automatically creates an `ownscale-agent`
Headscale user and an `ownscale-agent` machine. These show up in your
listings marked as internal resources. The machine is an embedded Tailscale
client running inside the ownscale server, and it forwards funnel traffic
into your tailnet. Removing it deletes all of the organization's funnel
routes. See [Funnels]({{< relref "features/funnels.md" >}}).

## Is ownscale the same as Tailscale?

No. ownscale is a self-hosted *coordination server* that the official
Tailscale clients connect to instead of Tailscale's hosted service. It's built
on [Headscale](https://headscale.net) and isn't affiliated with Tailscale
Inc. See [How it works]({{< relref "concepts/how-it-works.md" >}}).

## Does my traffic go through the ownscale server?

No. Machines connect directly to each other with WireGuard. When a direct
connection isn't possible, encrypted traffic goes through a
[DERP relay]({{< relref "concepts/terminology.md#derp" >}}). Only
[funnel]({{< relref "features/funnels.md" >}}) traffic passes through the
ownscale server.

## What's the difference between the console URL, the login server and the tailnet name?

- **Console**: `https://console.<BASE_DOMAIN>`, where you manage everything
  in a browser.
- **Login server**: `https://<org>.<BASE_DOMAIN>`, which you pass to
  `tailscale up --login-server`.
- **Tailnet name**: the DNS suffix of your machines' MagicDNS names, such as
  `net3k9x2a.os.example.com`.

See [Domains and URLs]({{< relref "reference/domains-and-urls.md" >}}).

## How do I enable Tailscale SSH?

Tailscale SSH is controlled by the `ssh` section of your organization's access
control policy (Access Control in the console). For example, to let every user
SSH into their own machines:

```jsonc
"ssh": [
  {
    "action": "accept",
    "src": ["autogroup:member"],
    "dst": ["autogroup:self"],
    "users": ["autogroup:nonroot", "root"],
  },
],
```

`src` and `dst` can't be `*`: use `autogroup:member`, `autogroup:self`,
`autogroup:tagged`, groups, users or tags. The `acls` must also allow port 22
between the machines.

Each machine you want to SSH into must run `tailscale set --ssh`. Then connect
with `ssh user@machine` from another machine in the tailnet. See
[Tailscale SSH]({{< relref "features/tailscale-ssh.md" >}}).

## How do I resolve internal domains with split DNS?

Split DNS sends queries for a domain, and its subdomains, to specific
nameservers instead of the global ones. Use it to resolve private names such
as `corp.example.com` from an internal DNS server.

Add the domain and its nameservers under Split DNS on the DNS page.
Nameservers can be IP addresses or `https://` DNS-over-HTTPS URLs. A
nameserver inside your network is usually reached through a machine that
advertises its subnet route, or is itself a machine in the tailnet.

Machines only use it when they accept DNS settings from the tailnet
(`tailscale set --accept-dns`, the default). See
[Split DNS]({{< relref "features/dns.md#split-dns" >}}).

## Why can't my teammate see my machines?

Each account belongs to one organization. If your teammate signed in without
an invite, they got their own organization. Remove their account and
[invite them]({{< relref "features/invites.md" >}}).

## Why can't machines reach each other?

Check, in order:

1. Both machines are online on **Machines** and not expired.
2. The [access control policy]({{< relref "features/access-control.md" >}})
   allows the traffic. Use the editor's tests to check.
3. The service listens on the tailnet interface, or on all interfaces.
4. `tailscale ping <peer>` succeeds from one to the other.

## Why does a machine show "Expired"?

Its node key expired, and someone has to sign in on it again with
`tailscale up --login-server=…`. For servers, disable key expiry from the
machine's menu.

## Why is the subnet route or exit node not working?

Advertised routes must be approved by a manager on the machine's **Routes**
dialog, and Linux clients must run `tailscale set --accept-routes`. See
[Subnet routers and exit nodes]({{< relref "features/subnet-routers-and-exit-nodes.md" >}}).

## Why can't I get an HTTPS certificate?

The server needs a `DNS_PROVIDER`, and the organization needs MagicDNS on.
The DNS page shows the status. See
[HTTPS certificates]({{< relref "features/https-certificates.md" >}}).

## Can I use the network_admin, it_admin or auditor roles?

You can assign them, but they currently have the same permissions as
`member`. See [Users and roles]({{< relref "features/users-and-roles.md" >}}).

## Can I move my existing Headscale server to ownscale?

There's no import tool yet. Create an organization and register the machines
again. The policy file format is the same, so you can paste your existing
policy into **Access Control**.
