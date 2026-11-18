---
title: Documentation
linkTitle: Top
aliases:
  - /docs/get-started/
  - /docs/getting-started/
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

## Relation with Tailscale and Headscale

**Tailscale** is the original proprietary[^proprietary] mesh VPN built on WireGuard.
**Headscale** is an open-source alternative to Tailscale. It runs on your server
and is configured with files and a CLI. **Ownscale** builds upon Headscale to
offer a web interface and the ability to host several tailnets on the same
server.

Common points:
- Tailscale, Headscale and Ownscale are server components. For day to day
usage users will interact with the Tailscale client applications for Linux,
MacOS, Windows, iOS, Android which are all compatible with the servers.

- Your traffic never goes through Ownscale. Machines talk to each other
directly, or through a DERP relay when a direct path isn't possible.

## Where to start

{{< cards >}}
  {{< card link="/docs/self-hosting/" title="Self-hosting" icon="server" subtitle="Run ownscale on your own server." >}}
  {{< card link="/docs/ownscalenet/" title="ownscale.net" icon="play" subtitle="Try it without hosting it." >}}
  {{< card link="/docs/connect/" title="Connecting devices" icon="device-mobile" subtitle="Point Tailscale clients at your login server." >}}
  {{< card link="/docs/reference/" title="Reference" icon="document-text" subtitle="CLI, domains, limitations and terminology." >}}
  {{< card link="/docs/faqs/" title="FAQs" icon="question-mark-circle" subtitle="Answers to common questions." >}}
{{< /cards >}}

[^proprietary]: Tailscale has many open-source components, including its
    clients, but its coordination server is closed-source.
