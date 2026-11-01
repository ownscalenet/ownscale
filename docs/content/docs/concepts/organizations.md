---
title: Organizations
weight: 3
---

An **organization** is ownscale's unit of tenancy. Each organization is an
independent [tailnet]({{< relref "terminology.md#tailnet" >}}) with its own:

- users and roles;
- machines, auth keys and funnels;
- access control policy;
- DNS settings and MagicDNS tailnet name;
- Headscale instance and database.

Nothing is shared between organizations. A machine in one organization can't
see or reach machines in another.

## Subdomain and login server

Each organization gets a subdomain of the server's base domain, for example
`abcd.example.com`. That URL is the organization's
[login server]({{< relref "terminology.md#login-server" >}}): Tailscale
clients use `tailscale up --login-server=https://abcd.example.com` to join
it. Subdomains are made of 4 to 63 lowercase letters and digits, and they're
picked by ownscale when the organization is created.

The console is shared by all organizations at `https://console.<BASE_DOMAIN>`.
After you sign in, it shows the organization your account belongs to.

## How organizations are created

When someone signs in for the first time **without** an invite, ownscale
creates a new organization and makes them its **owner**. When someone signs
in through an [invite]({{< relref "/docs/features/invites.md" >}}), they join
the inviting organization with the role chosen in the invite instead.

Each account belongs to exactly one organization.

## Sleeping organizations

An organization's Headscale instance stops when no machine has been
connected to it and nothing has used it for 30 minutes. This saves memory on
servers with many organizations. It starts again on the next request to its
login server or the console, so machines that come back online reconnect
automatically.

Because a sleeping organization has no machines online, its
[funnels]({{< relref "/docs/features/funnels.md" >}}) answer
`503 Service Unavailable`. Funnel requests don't wake the organization, so
strangers on the internet can't keep it running.

## Settings changes restart the instance

Changing DNS settings or [auth restrictions]({{< relref "/docs/features/auth-restrictions.md" >}})
restarts the organization's Headscale instance. Machines reconnect within
seconds, and established connections between machines keep working during
the restart.
