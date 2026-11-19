---
title: FAQs
weight: 70
---

## ownscale-agent user & machine

When creating a funnel, ownscale automatically creates an `ownscale-agent`
Headscale user and an `ownscale-agent` machine. These show up in your
listings marked as internal resources. The machine is an embedded Tailscale
client running inside the ownscale server, and it forwards funnel traffic
into your tailnet. Removing it deletes all of the organization's funnel
routes.

## Is ownscale the same as Tailscale?

No. ownscale is a self-hosted *coordination server* that the official
Tailscale clients connect to instead of Tailscale's hosted service. It's built
on [Headscale](https://headscale.net) and isn't affiliated with Tailscale
Inc.

## How does ownscale compare to NetBird?

Both are self-hosted mesh VPNs built on WireGuard. ownscale's strengths:
works with the official Tailscale clients, can  host many tailnets on
one small server, passwordless sign-in with passkeys, continuous backups,
BSD 3-Clause license.

Netbird positives that are missing from ownscale: device posture checks,
audit log, API tokens and a Terraform provider for automation, the
visual policy editor.

## What's the difference between the console URL, the login server and the tailnet name?

- **Console**: `https://console.<BASE_DOMAIN>`, where you manage everything
  in a browser. [Open the console](/console/).
- **Login server**: `https://<org>.<BASE_DOMAIN>`, which you pass to
  `tailscale up --login-server`.
- **Tailnet name**: the DNS suffix of your machines' MagicDNS names, such as
  `net3k9x2a.example.com`.

## How do I enable Tailscale SSH?

See [SSH into machines]({{< relref "use-cases/ssh.md" >}}).

## How do I resolve internal domains with split DNS?

See [Resolve internal domains]({{< relref "use-cases/split-dns.md" >}}).

## Why can't my teammate see my machines?

Each account belongs to one organization. If your teammate signed in without
an invite, they got their own organization. Create one on
[Invitations](/console/invitations) and send them the link: signing in
through it adds them to your organization.

## Why can't machines reach each other?

Check, in order:

1. Both machines are online on **Machines** and not expired.
2. The access control policy
   allows the traffic. Use the editor's tests to check.
3. The service listens on the tailnet interface, or on all interfaces.
4. `tailscale ping <peer>` succeeds from one to the other.

## Why does a machine show "Expired"?

Its node key expired, and someone has to sign in on it again with
`tailscale up --login-server=…`. For servers, disable key expiry from the
machine's menu.

## Why is the subnet route or exit node not working?

Routes and exit nodes must be approved in the machine's **Edit route
settings**, and Linux clients must run `tailscale set --accept-routes`. See
[Reach devices at home]({{< relref "use-cases/home-network.md" >}}) and
[Use an exit node]({{< relref "use-cases/exit-node.md" >}}).

## Can I move my existing Headscale server to ownscale?

There's no import tool yet. Create an organization and register the machines
again. The policy file format is the same, so you can paste your existing
policy into **Access Control**.
