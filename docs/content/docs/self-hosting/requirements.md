---
title: Requirements
weight: 1
---

## Server

- A Linux host with **Docker** and **Docker Compose**.
- **linux/amd64**: release images are only built for this architecture.
- A public IP address with inbound **TCP 80**, **TCP 443** and **UDP 443**
  open. Caddy uses all three.
- Persistent disk for `OWNSCALE_DATA_DIR` (default `./data`). ownscale's own database and every
  organization's database live there.

ownscale is designed to run many organizations on a small server. Each
organization's Headscale instance runs in-process and is stopped after 30
minutes of inactivity.

## Domains

You need a domain you control, called `BASE_DOMAIN` (for example
`example.com`), with a **wildcard DNS record** pointing at the server:

| Record | Points to |
| --- | --- |
| `example.com` | server IP (documentation site) |
| `*.example.com` | server IP (console, organizations, funnels) |

A single record per hostname isn't enough, because each organization gets its
own subdomain.

We recommend a second, separate registrable domain for user content, called
`CONTENT_DOMAIN` (for example `example-content.net`, not
`content.example.com`). Funnels and machine certificate names live there, so
content published by users can't set cookies on or make requests as the
console. It needs its own wildcard record. See
[Domains and URLs]({{< relref "/docs/reference/domains-and-urls.md" >}}).

## DNS provider

Ownscale gets wildcard certificates through an ACME DNS-01 challenge.
The provider must host the zones of `BASE_DOMAIN` and `CONTENT_DOMAIN`.
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

Callback URLs have the form `https://console.<BASE_DOMAIN>/auth/<provider>/callback`.

## Optional

- **Backup storage**: an S3 or S3-compatible bucket, an Azure Blob Storage
  container, or a local path. See
  [`osa restore`]({{< relref "/docs/reference/osa-cli.md#restore" >}}).
- **A separate small server** for a [custom DERP relay](https://tailscale.com/kb/1118/custom-derp-servers).
