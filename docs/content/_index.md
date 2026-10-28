---
title: ownscale
---

Ownscale is a self-hosted Tailscale control plane with a web console, built on
[Headscale](https://github.com/juanfont/headscale). Ownscale users are grouped
into organizations, each with its own set machines, routers and settings.

## Features

- multiple tailnets on the same ownscale instance
- web UI for headscale
- login with OIDC, passkeys, Google, Github, etc.
- funnels: expose local running servers through the internet
- all headscale features: MagicDNS, Split DNS, Taildrive, Taildrop, tags,
subnet routers, exit nodes, ACLs, Grants, Tailscale SSH and
[many more](https://headscale.net/stable/about/features/).

## Next steps

- Try it on the demo instance: ownscale.net
- Try it on your server. See [Installation]({{< relref "docs/installation.md" >}})
  for prerequisites and a Docker Compose quick start.

