---
title: Get started
weight: 1
aliases:
  - /docs/getting-started/
---

Ownscale is a self-hosted control plane and web console for Tailscale
clients. It is built on [Headscale](https://headscale.net), the open-source
implementation of the Tailscale coordination server. This documentation
relies on the concepts introduced in the Tailscale documentation.

## Relation with Headscale and Tailscale

**Tailscale** is the original proprietary mesh VPN built on WireGuard.
**Headscale** is an open-source alternative to Tailscale. It runs on your server
and is configured with files and a CLI. **Ownscale** builds upon Headscale to
offer a web interface and the ability to host several tailnets on the same
server.

Common points:
- Tailscale, Headscale and Ownscale are server components. For day to day
usage users will interact with the Tailscale client applications for Linux,
MacOS, Windows, iOS, Android which are all compatible with the servers.

- Your traffic never goes through Ownscale. Machines talk to each other
directly, or through a [DERP relay]({{< relref "/docs/concepts/terminology.md#derp" >}})
when a direct path isn't possible. See
[How it works]({{< relref "/docs/concepts/how-it-works.md" >}}).

## Where to start

{{< cards >}}
  {{< card link="/docs/self-hosting/" title="Self-hosting" icon="server" subtitle="Run ownscale on your own server." >}}
  {{< card link="/docs/ownscalenet/" title="ownscale.net" icon="play" subtitle="Try it without hosting it." >}}
{{< /cards >}}
