---
title: Deploy settings
weight: 40
---

Ownscale is configured with environment variables in the `.env` file next to
`compose.yaml`. Every setting is documented in
[`example.env`](https://github.com/ownscalenet/ownscale/blob/master/example.env),
grouped as:

- **Required**: the domain, the session secret and the reverse proxy.
- **DNS provider** (optional): wildcard and device certificates.
- **Recommended**: the release, data directory, [content
  domain](/docs/self-hosting/content-domain/), organization
  limits, login providers and backups.
- **Optional**: settings whose defaults suit most installations, such as
  funnel limits, the DERP map and logging.

The [install script]({{< relref "/docs/self-hosting/installation.md#install-script" >}})
copies `example.env` to `.env` and fills in the required settings. After
changing `.env`, apply it with `make up`.

When upgrading, compare the new release's `example.env` with your `.env` for
new settings.
