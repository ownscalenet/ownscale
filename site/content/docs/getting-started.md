---
title: Getting Started
weight: 1
---

# Getting Started

Welcome to the ownscale documentation.

ownscale is a self-hosted control plane for Tailscale/Headscale. This
section will grow to cover installation, configuration, and day-to-day
operation.

## SSO

ownscale federates login to a configurable upstream OIDC provider (Google,
Okta, a self-hosted Dex instance, etc. -- see `OWNSCALE_AUTH_UPSTREAM_OIDC_*`)
and exposes its own OIDC provider at the bare base domain
(`https://<base-domain>`, where the Ownscale console and login also live) for
each organization's Headscale containers. This documentation
site itself lives at `docs.<base-domain>`. You sign in with the upstream
provider once, then downstream admin surfaces can reuse that ownscale
session for single sign-on.

## Next steps

- Add more pages under `content/docs/`.
- Rebuild this site with `make docs` from the repository root whenever you
  change content, then commit the regenerated `etc/caddy/site/` output.
