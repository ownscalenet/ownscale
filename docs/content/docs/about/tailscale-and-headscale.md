---
title: ownscale, Tailscale and Headscale
linkTitle: Tailscale and Headscale
weight: 30
---

**Tailscale** is the original proprietary[^proprietary] mesh VPN built on WireGuard.
**Headscale** is an open-source alternative to Tailscale. It runs on your server
and is configured with files and a CLI. **Ownscale** builds upon Headscale to
offer a web interface and the ability to host several tailnets on the same
server.

Common points:
- Tailscale, Headscale and Ownscale are server components. For day to day
usage users interact with the Tailscale client applications for Linux,
MacOS, Windows, iOS, Android which are all compatible with the servers.

- Your traffic never goes through Ownscale. Machines talk to each other
directly, or through a DERP relay when a direct path isn't possible.

See [Limitations]({{< relref "/docs/reference/limitations.md" >}}) for what
works differently.

[^proprietary]: Tailscale has many open-source components, including its
    clients, but its coordination server is closed-source.
