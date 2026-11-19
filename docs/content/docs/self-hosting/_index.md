---
title: Self-hosting
weight: 20
---

Run ownscale on your own server. Unless specified, the documentation here
assumes you're going to run Ownscale with the provided Caddy image as
a reverse proxy.
If you already have a reverse proxy, please see the [bring your own reverse
proxy examples](/docs/self-hosting/byorp).
Ownscale behavior can be controlled through environment variables and
the [`example.env`](https://github.com/ownscalenet/ownscale/blob/master/example.env)
file is the reference documentation for all possible settings.

{{< cards >}}
  {{< card link="/docs/self-hosting/requirements/" title="Requirements" icon="clipboard-check" subtitle="Server, domains, DNS provider and login provider" >}}
  {{< card link="/docs/self-hosting/installation/" title="Installation" icon="download" subtitle="Install script, first sign-in and upgrades" >}}
  {{< card link="/docs/self-hosting/byorp/" title="Own reverse proxy" icon="switch-horizontal" subtitle="Run ownscale behind nginx, Traefik or another proxy" >}}
{{< /cards >}}
