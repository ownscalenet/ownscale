---
title: Connectivity and ports
weight: 40
---

## Direct connections and relays

Machines connect to each other directly over WireGuard, punching through NAT
when they can. When they can't, for example behind strict firewalls, their
traffic goes through a **DERP relay**. It stays end-to-end encrypted: the
relay can't read it.

By default ownscale uses Tailscale's public DERP servers. To use your own,
run a [DERP server](https://tailscale.com/kb/1118/custom-derp-servers), serve
a DERP map listing it, and set `OWNSCALE_DERP_MAP_URL`.

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
