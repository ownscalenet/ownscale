---
title: Expose a service to the internet
weight: 60
---

A funnel gives a service on one of your devices a public HTTPS address,
with no ports to open on that device.

1. On the [Funnels](/console/funnels) page, choose a **Subdomain**, the
   **Device** and the **Port** the service listens on.
2. The service is now public at `https://<subdomain>-<org>.<CONTENT_DOMAIN>`
   (see [content domain](/docs/self-hosting/content-domain/)).

`tailscale funnel` doesn't work with Ownscale; use this page instead.

Funnels are open to anyone with the address. The service must handle its own
authentication, and it must speak plain HTTP: Ownscale adds the HTTPS.

The first funnel creates an internal `ownscale-agent` device that forwards
the traffic; see the
[FAQ]({{< relref "/docs/faqs.md#ownscale-agent-user--device" >}}).
