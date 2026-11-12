---
title: Domains and URLs
weight: 3
---

With `BASE_DOMAIN=example.com` and `CONTENT_DOMAIN=example-content.net`:

| URL | Serves |
| --- | --- |
| `https://example.com` | This documentation site. |
| `https://example-content.net` | A short page explaining what the content domain is. |
| `https://console.example.com` | The web console, its API and ownscale's OIDC provider. |
| `https://console.example.com/invite/<token>` | Invite links. |
| `https://<org>.example.com` | The organization's login server (its Headscale instance). Used with `tailscale up --login-server`. |
| `https://<label>-<org>.example-content.net` | A funnel for exposing local services to the internet. |
| `<machine>.<tailnet label>.os.example-content.net` | A machine's MagicDNS name, used for HTTPS certificates. Only resolvable inside the tailnet. |
| `https://example.com/s/<slug>` | Short links into the documentation, used by the console. |
| `https://example.com/console/<path>` | Redirects to `https://console.example.com/<path>`, so the docs can link to console pages. |

Without a separate `CONTENT_DOMAIN`, funnels and machine names use
`example.com` too.

## Naming rules

| Name | Rules |
| --- | --- |
| Organization subdomain | 4 to 63 lowercase letters and digits. Chosen by ownscale. `admin`, `auth`, `console`, `derp`, `docs` and `www` are reserved. |
| Funnel label | 1 to 63 lowercase letters and digits, no hyphens, unique per organization. |
| Tailnet label | Random, chosen when the organization is created, and can't be changed. |

Hyphens aren't allowed in organization subdomains or funnel labels, so a
funnel's `<label>-<org>` host can't be confused with another one.
