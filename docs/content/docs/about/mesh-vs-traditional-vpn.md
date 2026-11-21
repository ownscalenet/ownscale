---
title: Mesh vs Traditional VPN
weight: 20
---

## Traditional VPN

A traditional VPN sends every device's traffic through one central server.
Such a setup can work well in many situations, but there could be
challenges related to:

- **Bottlenecks**: all traffic crosses the server, adding latency and bandwidth
  costs, even between two devices in the same office.
- **Single point of failure**: when the server is down, nobody can work.
- **All or nothing**: once connected, a device is usually trusted with the
  whole network.
- **Hard to run**: firewall rules, port forwarding and certificates need
  constant care.

## Mesh VPN

In a mesh, devices connect to each other directly. ownscale is the
**coordination server**: it tells each device which peers it may talk to and
how to reach them, but the traffic itself never passes through it.

| | Traditional VPN | Mesh with ownscale |
| --- | --- | --- |
| Traffic path | through the VPN server | direct between devices |
| If the server goes down | everything stops | existing connections keep working |
| Access | the whole network | per device and port, from the [access control policy](/console/access-control) |
| Behind NAT | needs port forwarding | NAT traversal, relay as fallback |
| Internet traffic | often all of it goes through the office | stays on the local connection, unless you pick an [exit node]({{< relref "/docs/use-cases/exit-node.md" >}}) |
| Changing networks | the client reconnects | connections carry on, e.g. from Wi-Fi to mobile data |

When no direct path exists, traffic goes through a
[DERP relay]({{< relref "connectivity.md" >}}), still end-to-end encrypted.

## When a traditional VPN still fits

- **Inspecting or logging all traffic**, for compliance or content filtering.
  A mesh has no central point that sees every connection, and ownscale keeps
  no traffic logs.
- **One outgoing IP address** that partners can allowlist. In a mesh, route
  that traffic through an [exit node]({{< relref "/docs/use-cases/exit-node.md" >}})
  or a [subnet router]({{< relref "/docs/use-cases/home-network.md" >}}).
- **Devices that can't run a client**, such as printers, cameras or old
  servers. A mesh needs the Tailscale client on every device; reach the others
  through a [subnet router]({{< relref "/docs/use-cases/home-network.md" >}}).

## Why not plain WireGuard?

The tunnels are [WireGuard](https://www.wireguard.com), which is fast and
simple. Run by hand, though, every device needs a key pair, a unique address,
every peer's public key and every peer's endpoint, and all of it must be
updated whenever a device is added or moves.

The Tailscale clients and ownscale automate this: keys are generated on each
device and private keys never leave it, addresses are assigned on
registration, and peers' keys and endpoints are pushed to every device as they
change.

## Moving from a traditional VPN

You don't have to switch everything at once. Run a [subnet
router]({{< relref "/docs/use-cases/home-network.md" >}}) inside the network
the old VPN serves, so devices on the tailnet reach it too. Then move devices
over one at a time, and retire the old VPN when none use it.
