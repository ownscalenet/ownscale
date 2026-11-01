---
title: Limitations
weight: 4
---

ownscale uses Headscale, so most of what Headscale supports works, and
Headscale's [feature list](https://headscale.net/stable/about/features/)
applies. This page lists where ownscale differs from Tailscale's hosted
service or from a standalone Headscale.

## Compared with Tailscale

- **Funnel**: `tailscale funnel` doesn't work. Use ownscale's
  [funnels]({{< relref "/docs/features/funnels.md" >}}) instead, which are
  HTTP only.
- **Sharing machines between tailnets** (node sharing) isn't supported.
- **Tailnet lock**, **app connectors**, **device posture** and **logging and
  audit streaming** aren't available.
- Some newer policy features may not be supported by Headscale yet. The
  editor rejects a policy it can't apply.

## Compared with Headscale

- **No config file, no Headscale API keys.** Each organization's settings are
  edited in the console. Headscale's CLI is still available through
  [`osa headscale`]({{< relref "osa-cli.md#headscale" >}}).
- **Fixed settings**: the IP ranges `100.64.0.0/10` and `fd7a:115c:a1e0::/48`,
  sequential IP allocation, the ephemeral node timeout (120 s) and Taildrop
  (always on).
- **No embedded DERP server.** All organizations share the DERP map from
  `OWNSCALE_DERP_MAP_URL`.
- **Global nameservers are fallbacks only.** ownscale doesn't set Headscale's
  `override_local_dns`.
- **The tailnet name can't be changed.**
- **Settings changes restart the instance.** Saving DNS settings or auth
  restrictions restarts the organization's Headscale. Policy changes don't.

## ownscale itself

- **Roles**: `network_admin`, `it_admin` and `auditor` currently have the
  same permissions as `member`.
- **Invites** aren't emailed. Share the link yourself.
- **One organization per account.** To move to another organization, the
  account must be removed from its current one.
- **Funnels**: HTTP only to the target, 32 MiB request bodies by default, and
  no response while the organization is asleep.
- **Machine certificates** are issued by Let's Encrypt. Names are public in
  Certificate Transparency logs, and all organizations share a limit of 50
  new certificates per registered domain per week.
- **Backups** replicate on an interval, so the last few seconds of changes
  can be lost if the server dies.
- **Release images** are linux/amd64 only.
