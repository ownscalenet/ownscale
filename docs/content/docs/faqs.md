---
title: FAQs
weight: 3
---

# FAQs

## ownscale-agent user & machine

When creating a funnel, ownscale automatically creates an `ownscale-agent`
headscale user and an `ownscale-agent` machine. These show up in your
listings marked as internal resources.

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

## How do I resolve internal domains with split DNS?

Split DNS sends queries for a domain, and its subdomains, to specific
nameservers instead of the global ones. Use it to resolve private names such
as `corp.example.com` from an internal DNS server.

Add the domain and its nameservers under Split DNS on the DNS page.
Nameservers can be IP addresses or `https://` DNS-over-HTTPS URLs. A
nameserver inside your network is usually reached through a machine that
advertises its subnet route, or is itself a machine in the tailnet.

Machines only use it when they accept DNS settings from the tailnet
(`tailscale up --accept-dns`, the default).
