---
title: Self-hosting
weight: 5
---

Run ownscale on your own server. Unless specified, the documentation here
assumes you're going to run Ownscale with the provided Caddy image as
a reverse proxy.
If you already have a reverse proxy, please see the [bring your own reverse
proxy examples](/docs/self-hosting/byorp).

{{< cards >}}
  {{< card link="/docs/self-hosting/requirements/" title="Requirements" icon="clipboard-check" subtitle="Server, domains, DNS provider and login provider." >}}
  {{< card link="/docs/self-hosting/installation/" title="Installation" icon="download" subtitle="Install script or manual Docker Compose setup." >}}
  {{< card link="/docs/self-hosting/byorp/" title="Own reverse proxy" icon="switch-horizontal" subtitle="Run ownscale behind nginx, Traefik or another proxy." >}}
{{< /cards >}}

Every setting is documented in `example.env`, which ships with each release.
