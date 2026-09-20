---
title: Introduction
type: docs
---

# ownscale

**ownscale is a self-hosted control plane and web console for Headscale.**
It runs and manages one Headscale instance per organization for you, and
gives every user in that organization a browser-based console to manage
their network.

## Features

- **Multi-tenant organizations**: provision any number of independent
  tailnets (organizations), each with its own Headscale instance and
  subdomain, from a single ownscale deployment.
- **Web console**: manage machines, users, access control policies and
  DNS settings from a browser.
- **Funnel**: expose an internal service on your tailnet to the public
  internet over HTTPS, without a public IP or port-forwarding.
- **Invite-based onboarding**: invite teammates to an organization by
  email; they self-register through the console.
- **Single sign-on**: ownscale federates login to any standard OIDC
  provider (Google, Okta, a self-hosted Dex instance, etc.).
- **Access control (ACLs)**: edit each organization's ACL policy
  directly from the console.
- **Custom DNS**: configure per-organization DNS settings (MagicDNS,
  nameservers, search domains).

This is the documentation portal for ownscale. Head over to
[Getting Started]({{< relref "docs/getting-started.md" >}}) to begin, or
jump straight to [Installation]({{< relref "docs/installation.md" >}}) if
you're ready to self-host it.
