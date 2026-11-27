---
title: Content domain
weight: 40
---

The content domain, set with `CONTENT_DOMAIN`, is a second domain that serves
what your users publish, away from `BASE_DOMAIN` and the Ownscale console.
It's optional, but very much recommended for any server other people use.

## What it serves

With `CONTENT_DOMAIN=example-content.net`:

| Host | Serves |
| --- | --- |
| `<label>-<org>.example-content.net` | [Funnels](/docs/use-cases/funnels/) |
| `<device>.<tailnet label>.example-content.net` | Devices' [MagicDNS](/docs/reference/terminology/#magicdns) names and their certificates |

Every organization gets a random tailnet label that can't be changed, so
device names are unique and don't reveal the organization's subdomain.

## Why a separate domain

Funnels let anyone with a device publish a website. If that website were
served from the same registrable domain as the console, it could set cookies
for the console's domain and send requests that look like they come from it.
A shared domain also shares its search engine and blocklist reputation: one
user hosting phishing or malware on a funnel can get your whole domain,
console included, flagged. A subdomain of `BASE_DOMAIN` is not enough;
a separate domain is required.

## Setting it up

1. Register the domain and add a **wildcard DNS record** pointing at the
   server, plus a record for the domain itself:

   | Record | Points to |
   | --- | --- |
   | `example-content.net` | server IP |
   | `*.example-content.net` | server IP |

2. Set it in `.env` and apply it with `make up`:

   ```sh
   CONTENT_DOMAIN=example-content.net
   ```

3. If you use a [DNS provider](/docs/self-hosting/requirements/#dns-provider-optional),
   it must host the content domain's zone as well as `BASE_DOMAIN`'s. Device
   certificate challenges are written to the content domain's zone. If the
   content domain is a subdomain of a zone the provider hosts, set
   `DNS_ZONE` to that zone.

If you [run your own reverse proxy](/docs/self-hosting/byorp/), it must also
route `example-content.net` and `*.example-content.net` to Ownscale and have
certificates for them.
