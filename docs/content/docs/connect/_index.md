---
title: Connecting devices
weight: 1
aliases:
  - /docs/connecting-devices/
---

ownscale works with the official Tailscale clients. The only difference from
using Tailscale's hosted service is that each client must be told to use your
organization's **login server**, `https://<org>.<BASE_DOMAIN>`, instead of
Tailscale's servers. The [console](/console/) shows this URL in its header.
