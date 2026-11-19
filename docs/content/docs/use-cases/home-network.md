---
title: Reach devices at home
weight: 10
---

Devices that can't run Tailscale, such as a NAS, printer or camera, are
reached through a **subnet router**: one machine on the home network that
forwards tailnet traffic to the rest of it.

1. On a Linux machine at home, advertise the home network, for example:

   ```sh
   tailscale set --advertise-routes=192.168.1.0/24
   ```

2. In the console, open **Machines**, then that machine's menu, **Edit route
   settings**, and approve the route.
3. On Linux machines that should use it, run
   `tailscale set --accept-routes`. Other platforms accept routes by default.

Devices on the home network are now reachable by their local addresses, such
as `192.168.1.20`.

Pick a subnet that's unlikely to clash with the networks you connect from.
`192.168.0.0/24` and `192.168.1.0/24` are common on hotel and café Wi-Fi.
