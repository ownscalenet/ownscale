---
title: Expose a service to the internet
weight: 60
---

A funnel gives a service on one of your machines a public HTTPS address,
with no ports to open on that machine.

1. On the [Funnels](/console/funnels) page, choose a **Subdomain**, the
   **Machine** and the **Port** the service listens on.
2. The service is now public at `https://<subdomain>-<org>.<CONTENT_DOMAIN>`.

`tailscale funnel` doesn't work with ownscale; use this page instead.

Funnels are open to anyone with the address. The service must handle its own
authentication, and it must speak plain HTTP: ownscale adds the HTTPS.

The first funnel creates an internal `ownscale-agent` machine that forwards
the traffic; see the
[FAQ]({{< relref "/docs/faqs.md#ownscale-agent-user--machine" >}}).
