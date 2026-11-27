---
title: Connect two sites
weight: 20
---

To let devices in two offices reach each other, run a [subnet
router]({{< relref "home-network.md" >}}) in each, advertising its own
network:

```sh
# Office A
tailscale set --advertise-routes=10.1.0.0/24 --accept-routes
# Office B
tailscale set --advertise-routes=10.2.0.0/24 --accept-routes
```

Approve both routes in **Edit route settings** on each device.

Devices that don't run Tailscale send traffic for the other office to their
default gateway, so add a static route on each office's router: on office A,
`10.2.0.0/24` via the subnet router's local address, and the reverse on
office B. The two networks must not overlap.

See Tailscale's [site-to-site
guide](https://tailscale.com/kb/1214/site-to-site) for the Linux forwarding
settings.
