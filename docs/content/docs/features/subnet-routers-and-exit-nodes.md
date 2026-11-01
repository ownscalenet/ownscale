---
title: Subnet routers and exit nodes
linkTitle: Subnets and exit nodes
weight: 3
---

Machines can forward traffic for others in two ways:

- A **subnet router** gives the tailnet access to a network whose devices
  don't run Tailscale, such as an office LAN, a home network or a cloud VPC.
- An **exit node** forwards *all* internet traffic of the machines that
  choose to use it.

Both follow the same two steps: the machine **advertises** what it can
route, and a manager **approves** it.

## Advertising

On the machine that will do the routing:

```sh
# subnet router
sudo tailscale set --advertise-routes=192.168.1.0/24,10.0.0.0/16

# exit node
sudo tailscale set --advertise-exit-node
```

On Linux, IP forwarding must be enabled first. See Tailscale's
[subnet router](https://tailscale.com/kb/1019/subnets) docs.

## Approving

**Who can do this:** managers.

Advertised routes appear under **Awaiting approval** in the
**Subnets and routing** section of the machine's detail page. Open the
machine's menu on **Machines**, choose **Routes**, and tick the routes to
approve. An exit node shows up as the routes `0.0.0.0/0` and `::/0`.

To skip manual approval, add `autoApprovers` to the policy:

```jsonc
"autoApprovers": {
  "routes": {
    "192.168.1.0/24": ["tag:router"],
  },
  "exitNode": ["tag:exit"],
},
```

## Using the routes

- **Linux** clients must opt in to subnet routes with
  `tailscale set --accept-routes`. Other platforms accept them by default.
- To use an exit node, run `tailscale set --exit-node=<name or IP>`, or pick
  it from the client's menu.

The policy must allow traffic to the routed addresses. For exit nodes that
means a destination of `autogroup:internet`:

```jsonc
{ "action": "accept", "src": ["group:travel"], "dst": ["autogroup:internet:*"] }
```

## High availability

If two subnet routers advertise the same route, Headscale uses one and
switches to the other within seconds if the first goes offline.

See Tailscale's [subnet router](https://tailscale.com/kb/1019/subnets) and
[exit node](https://tailscale.com/kb/1103/exit-nodes) docs.
