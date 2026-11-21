---
title: About
weight: 10
---

Ownscale is a self-hosted coordination server and web console for Tailscale
clients. It is built on [Headscale](https://headscale.net), an open-source
implementation of the Tailscale server. This documentation
relies on the concepts introduced in the
[Tailscale documentation](https://tailscale.com/docs).

## Features

**Many tailnets, one server**\
Organizations are used for splitting users into groups. Each organization
gets its own tailnet, backed by its own Headscale instance and subdomain.
Users from one organization can only access resources from their organization.

**Web console**\
Manage machines, users, access control, DNS and auth keys from the browser.

**Single sign-on and passkeys**\
Log in with OIDC, Google, Microsoft, GitHub or a passkey.

**Funnels**\
Expose a service running on a machine in your tailnet to the public internet.

**Tailscale features**\
MagicDNS, split DNS, subnet routers, exit nodes, tags, ACLs, grants,
Tailscale SSH, Taildrop, HTTPS certificates...

## Goals

Ownscale builds on Headscale's goal of an open-source alternative to
Tailscale, adding a way for self-hosters to run several tailnets from the
same backend and manage them through a web UI.

Because the development team is small, ownscale implements a smaller list of
features. Bug fixes and stable execution are prioritised over porting
Tailscale features.

{{< cards >}}
  {{< card link="/docs/about/how-it-works/" title="How ownscale works" icon="cog" subtitle="The pieces and how they fit together" >}}
  {{< card link="/docs/about/mesh-vs-traditional-vpn/" title="Mesh vs Traditional VPN" icon="share" subtitle="Why devices connect directly" >}}
  {{< card link="/docs/about/tailscale-and-headscale/" title="Tailscale and Headscale" icon="link" subtitle="How ownscale relates to both" >}}
  {{< card link="/docs/about/connectivity/" title="Connectivity and ports" icon="switch-horizontal" subtitle="Direct connections, relays and firewalls" >}}
{{< /cards >}}
