---
title: Get started
weight: 1
aliases:
  - /docs/getting-started/
---

Ownscale is a self-hosted control plane and web console for Tailscale
clients. It is built on [Headscale](https://headscale.net), the open-source
implementation of the Tailscale coordination server, and ships as a single Go
binary containing the server, Headscale, the console and this documentation.

## Tailscale, Headscale and Ownscale

- **Tailscale** is a VPN built on WireGuard. Its clients (the `tailscale` app
  on Linux, macOS, Windows, iOS, Android, etc.) connect your devices directly to
  each other. The company Tailscale also runs the *coordination server* that
  tells clients about each other, and it's closed source.
- **Headscale** is an open-source coordination server that the unmodified
  Tailscale clients can use instead. One Headscale instance serves one
  private network (one *tailnet*), and is configured with files and a CLI.
- **ownscale** runs many Headscale instances in a single process, one per
  [organization]({{< relref "/docs/concepts/organizations.md" >}}), and adds a
  web console, single sign-on, passkeys, roles, invites and
  [funnels]({{< relref "/docs/features/funnels.md" >}}).

Your traffic never goes through Ownscale. Machines talk to each other
directly, or through a [DERP relay]({{< relref "/docs/concepts/terminology.md#derp" >}})
when a direct path isn't possible. See
[How it works]({{< relref "/docs/concepts/how-it-works.md" >}}).

## Where to start

{{< cards >}}
  {{< card link="/docs/self-hosting/" title="Self-hosting" icon="server" subtitle="Run ownscale on your own server." >}}
  {{< card link="/docs/ownscalenet/" title="ownscale.net" icon="play" subtitle="Try it without hosting it." >}}
{{< /cards >}}
