---
title: Requirements
weight: 10
---

## Server

- A Linux host with **Docker** and **Docker Compose**.
- A public IP address with inbound **TCP 80**, **TCP 443** and **UDP 443**
  open. Caddy uses all three.
- Persistent disk for `OWNSCALE_DATA_DIR` (default `./data`). ownscale's own database and every
  organization's database live there.

ownscale is designed to run many organizations on a small server. Each
organization's Headscale instance runs in-process, is stopped after a few
minutes of inactivity, and starts again on the next request.

## Domains

You need a domain you control, called `BASE_DOMAIN` (for example
`example.com`), with a **wildcard DNS record** pointing at the server:

| Record | Points to |
| --- | --- |
| `example.com` | server IP (documentation site) |
| `*.example.com` | server IP (console, organizations, funnels) |

A single record per hostname isn't enough, because each organization gets its
own subdomain.

A second, separate domain for user content, called `CONTENT_DOMAIN`
is very much recommended. Ownscale serves user-provided content
through funnels and that allows setting cookies on the domain.
The content domain needs its own wildcard record.

## DNS provider (optional)

Without a DNS provider, Caddy gets a separate certificate for each
organization and funnel the first time it's visited.
Machine certificates (`tailscale cert`, `tailscale serve` over
HTTPS) are disabled.

With a provider set up, Caddy gets wildcard certificates through an ACME DNS-01 challenge,
and machines can get certificates. Use one if you expect many organizations or
funnels: Let's Encrypt issues at most 50 certificates per domain per week. The
provider must host the zones of `BASE_DOMAIN` and `CONTENT_DOMAIN`.
Supported providers:

- **Azure DNS**: a service principal with *DNS Zone Contributor* on the zone;
- **Cloudflare**: an API token with *Zone:Read* and *DNS:Edit*;
- **Hetzner DNS**: an API token;
- **RFC 2136**: dynamic updates to your own BIND, Knot, PowerDNS…, with a
  TSIG key allowed to update TXT records.

## Login providers

Users need at least one way to sign in. Passkeys always work. You can add
any of the following:

- an **OpenID Connect provider** such as Okta, Keycloak, Authentik, Dex,
  Google Workspace or Entra ID;
- **Google**, **Microsoft** or **GitHub** OAuth apps.

## Optional

- **Backup storage**: an S3 or S3-compatible bucket, an Azure Blob Storage
  container, or a local path. See
  [`osa restore`]({{< relref "/docs/reference/osa-cli.md#restore" >}}).
- **A separate small server** for a [custom DERP relay](https://tailscale.com/kb/1118/custom-derp-servers).
  By default, machines that can't connect directly relay their traffic,
  end-to-end encrypted, through Tailscale's public DERP servers. Set
  `OWNSCALE_DERP_MAP_URL` to use your own.
