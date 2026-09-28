---
title: FAQs
weight: 3
---

# FAQs

## Funnel machine

When creating a funnel, ownscale will automatically create a new user and a new
machine. These will show up in your listings marked as internal resources.

## How do I enable Tailscale SSH?

Tailscale SSH is controlled by the `ssh` section of your organization's access
control policy (Access Control in the console). For example, to let every user
SSH into their own machines:

```jsonc
"ssh": [
  {
    "action": "accept",
    "src": ["autogroup:member"],
    "dst": ["autogroup:self"],
    "users": ["autogroup:nonroot", "root"],
  },
],
```

`src` and `dst` can't be `*`: use `autogroup:member`, `autogroup:self`,
`autogroup:tagged`, groups, users or tags. The `acls` must also allow port 22
between the machines.

Each machine you want to SSH into must run `tailscale up --ssh`. Then connect
with `tailscale ssh user@machine` from another machine in the tailnet.
