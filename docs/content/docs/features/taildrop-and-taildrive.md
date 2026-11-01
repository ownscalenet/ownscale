---
title: Taildrop and Taildrive
weight: 6
---

## Taildrop

Taildrop sends files directly between machines over the tailnet. It's
enabled for every organization.

```sh
# send
tailscale file cp report.pdf my-laptop:

# receive (Linux)
sudo tailscale file get ~/Downloads
```

Desktop and mobile apps also show Taildrop in their share menus. Files can
only be sent between machines owned by the same user. See Tailscale's
[Taildrop docs](https://tailscale.com/kb/1106/taildrop).

## Taildrive

Taildrive shares folders from a machine as a WebDAV drive that other
machines can mount. It's off until the policy grants it. Allow sharing and
access with `nodeAttrs` and a `grants` rule with the
`tailscale.com/cap/drive` capability, for example:

```jsonc
"nodeAttrs": [
  { "target": ["autogroup:member"], "attr": ["drive:share", "drive:access"] },
],
"grants": [
  {
    "src": ["autogroup:member"],
    "dst": ["autogroup:self"],
    "app": { "tailscale.com/cap/drive": [{ "shares": ["*"], "access": "rw" }] },
  },
],
```

Then share a folder on the machine with `tailscale drive share`. See
Tailscale's [Taildrive docs](https://tailscale.com/kb/1369/taildrive) and
Headscale's [policy reference](https://headscale.net/stable/ref/policy/).
