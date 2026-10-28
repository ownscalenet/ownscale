---
title: Getting Started
weight: 1
---

# Getting Started

ownscale is a self-hosted control plane and web console for
[Headscale](https://github.com/juanfont/headscale), the open-source
implementation of the Tailscale coordination server. Ownscale comes packaged
as a single go binary containing the server, Headscale, the frontend and the
documentation.

## Features

- web UI for Headscale
- multiple tailnets on the same ownscale instance
- login with OIDC, passkeys, Google, Github, etc.
- funnels: expose local running servers through the internet
- all headscale features: MagicDNS, Split DNS, Taildrive, Taildrop, tags,
subnet routers, exit nodes, ACLs, Grants, Tailscale SSH and
[many more](https://headscale.net/stable/about/features/).

## Next steps

- Try it on the demo instance: [ownscale.net](https://console.ownscale.net)
- Try it on your server. See [Installation]({{< relref "docs/installation.md" >}})
  for prerequisites and a Docker Compose quick start.
- Already running? Sign in at `https://console.<base_domain>/login` to
  create your first organization, invite users, and manage machines,
  ACLs, and DNS.
- See the [FAQs]({{< relref "docs/faqs.md" >}}) for answers to common
  questions.
- Add more pages under `content/docs/`, and rebuild this site with
  `make docs` from the repository root whenever you change content.


## Organizations

Each user of ownscale belongs to an organization; that is an independent
tailnet backed by its own Headscale instance. Users, machines, ACLs,
and DNS settings are all scoped to a single organization.
Each organization gets its own subdomain, for example: `abc123.ownscale.net`.
Invitations allow other users to join the same organization.

## Single sign-on

ownscale supports login through well-known identity providers such as
Google, Github and Microsoft. Generic OIDC and passkeys are supported too.
Users from the same organization can use different authentication methods.

