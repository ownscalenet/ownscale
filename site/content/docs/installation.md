---
title: Installation
weight: 2
---

# Installation

ownscale ships as a Docker Compose stack: an `ownscale` container (the
console + API + per-organization Headscale management), a `caddy`
container (TLS termination and reverse proxying for the console, this
documentation site, and every organization's subdomain), and one
Headscale container per organization it provisions.

## Prerequisites

- **Docker and Docker Compose**, running on the host that will serve
  traffic.
- **A domain you control**, with a wildcard DNS record
  (`*.example.com`) pointing at this host. Each organization and Funnel
  gets its own subdomain (e.g. `acme.example.com`,
  `console.example.com`), so a single A/AAAA record per host isn't
  enough.
  - For local development, use a private,
    [RFC 2606](https://www.rfc-editor.org/rfc/rfc2606)-reserved TLD
    like `ownscale.test` instead of a real domain -- see
    [Local development](#local-development) below.
- **An OIDC identity provider** to authenticate against (Google, Okta,
  a self-hosted [Dex](https://dexidp.io/) instance, or any standard
  OIDC issuer). ownscale federates login to it rather than managing
  passwords itself.

## Quick start

```sh
git clone https://github.com/ownscalenet/ownscale.git
cd ownscale
cp .env.example .env   # edit BASE_DOMAIN, OWNSCALE_AUTH_*, etc. -- see below
docker compose up -d --build
```

Once the stack is up, sign in at `https://console.<base_domain>/login`
and create your first organization from the console.

## Core configuration

All configuration is via environment variables in `.env` (see
`.env.example` in the repository for the full, documented list). At
minimum, set:

- `BASE_DOMAIN` -- the wildcard domain covered by your DNS record
  (e.g. `example.com`). The console lives at `console.<BASE_DOMAIN>`,
  this documentation site at the bare `<BASE_DOMAIN>`, and each
  organization at `<subdomain>.<BASE_DOMAIN>`.
- `OWNSCALE_AUTH_SESSION_SECRET` -- a random string used to sign login
  session cookies. Required to start the server.
- `OWNSCALE_AUTH_UPSTREAM_OIDC_ISSUER`,
  `OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_ID`,
  `OWNSCALE_AUTH_UPSTREAM_OIDC_CLIENT_SECRET`,
  `OWNSCALE_AUTH_UPSTREAM_OIDC_CALLBACK_URL` -- your upstream OIDC
  provider's details. All four are required.
- `OWNSCALE_DATA_DIR` -- an absolute path where every SQLite database
  (ownscale's own, plus one per organization's Headscale instance) is
  stored.

## Local development

For local dev, use a reserved-TLD placeholder domain like
`ownscale.test` instead of a real one. Since it has no real wildcard
DNS, add an `/etc/hosts` entry per subdomain you actually use
(`ownscale.test`, `console.ownscale.test`, `dex.ownscale.test`, and one
per organization created). Also set `CADDY_LOCAL_CERTS=local_certs` so
Caddy issues certificates from its own internal CA instead of
attempting real ACME issuance, which always fails for non-public
domains.

The repository bundles a [Dex](https://dexidp.io/) instance for local
dev as a zero-setup upstream OIDC provider, with one seeded test login
(`admin@ownscale.test` / `ownscale`) -- `.env.example`'s
`OWNSCALE_AUTH_UPSTREAM_OIDC_*` defaults already point at it.

See the repository's `README.md` for full details on local development
(`make build`, `make dev-up`), production TLS (wildcard certificates
via Azure DNS-01), and trusting the local development CA in your OS or
browser.
