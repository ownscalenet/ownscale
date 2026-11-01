---
title: Self-hosting
weight: 5
---

Run ownscale on your own server. A deployment is a Docker Compose stack with
two containers, `ownscale` and `caddy`, plus DNS records and an optional
backup destination.

{{< cards >}}
  {{< card link="/docs/self-hosting/requirements/" title="Requirements" icon="clipboard-check" subtitle="Server, domains, DNS provider and login provider." >}}
  {{< card link="/docs/self-hosting/installation/" title="Installation" icon="download" subtitle="Docker Compose quick start." >}}
{{< /cards >}}

Every setting is documented in `example.env`, which ships with each release.
