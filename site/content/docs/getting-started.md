---
title: Getting Started
weight: 1
---

# Getting Started

Welcome to the ownscale documentation.

ownscale is a self-hosted control plane and web console for
[Headscale](https://github.com/juanfont/headscale), an open-source
implementation of the Tailscale coordination server. This section
covers the core concepts you'll run into once ownscale is installed;
head to [Installation]({{< relref "docs/installation.md" >}}) first if
you haven't deployed it yet.

## Organizations

Each organization is an independent tailnet, backed by its own
Headscale instance and reachable at its own subdomain (e.g.
`acme.<base_domain>`). Users, machines, ACLs, and DNS settings are all
scoped to a single organization.

## Single sign-on

ownscale federates login to a configurable upstream OIDC provider (Google,
Okta, a self-hosted Dex instance, etc. -- see `OWNSCALE_AUTH_UPSTREAM_OIDC_*`)
and exposes its own OIDC provider at the console subdomain
(`https://console.<base-domain>`, where the Ownscale console and login also
live) for each organization's Headscale instance. This documentation
site itself lives at the bare `<base-domain>`. You sign in with the upstream
provider once, then downstream admin surfaces can reuse that ownscale
session for single sign-on.

## Next steps

- Not installed yet? See [Installation]({{< relref "docs/installation.md" >}})
  for prerequisites and a Docker Compose quick start.
- Already running? Sign in at `https://console.<base_domain>/login` to
  create your first organization, invite users, and manage machines,
  ACLs, and DNS.
- See the [FAQs]({{< relref "docs/faqs.md" >}}) for answers to common
  questions.
- Add more pages under `content/docs/`, and rebuild this site with
  `make docs` from the repository root whenever you change content,
  then commit the regenerated `etc/caddy/site/` output.

