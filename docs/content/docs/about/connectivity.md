---
title: Connectivity and ports
weight: 40
---

## Direct connections and relays

Machines connect to each other directly over WireGuard, punching through NAT
when they can. When they can't, for example behind strict firewalls, their
traffic goes through a **DERP relay**. It stays end-to-end encrypted: the
relay can't read it.

Owners and admins choose the relays their organization's machines may use
under **Relays (DERP)** on the [DNS](/console/dns) page. Any combination of:

- **Use Tailscale servers**: Tailscale's public relays, around the world.
  This is the default.
- **Use Ownscale servers**: the relays run by ownscale.net.
- **Your own DERP servers**: a hostname for each
  [DERP server you run](https://tailscale.com/kb/1118/custom-derp-servers),
  with optional IPv4 and IPv6 addresses for machines to use instead of
  looking the hostname up.

> [!INFO]
> Tailscale has an extensive set of free to use DERP servers
> and you're most likely to get best experience by using
> their servers.

## Client firewalls

Machines need no inbound ports. Outbound, they need:

| Port | Used for |
| --- | --- |
| 443/tcp | The login server, and DERP relays |
| 3478/udp | STUN, to find their public address |
| UDP, any | Direct WireGuard connections. Clients listen on 41641 by default. |

Blocking outbound UDP still works, but all traffic then goes through relays,
which is slower.

## Server ports

With the bundled Caddy, open these inbound:

| Port | Used for |
| --- | --- |
| 80/tcp | Redirect to HTTPS, ACME challenges |
| 443/tcp | Console, login servers, funnels |
| 443/udp | HTTP/3 |
