---
title: DNS
weight: 20
---

The **DNS** page controls the DNS settings that ownscale pushes to every
machine in the organization. Machines use them when they accept DNS settings
from the tailnet, which is the default (`tailscale set --accept-dns=true`).

**Who can do this:** managers edit the settings. Members can view them.
Saving restarts the organization's Headscale instance, and connected machines
reconnect within seconds.

## Tailnet name

The DNS suffix of your machines' MagicDNS names, for example
`net3k9x2a.os.example.com`. ownscale picks a random label under
`os.<CONTENT_DOMAIN>` when the organization is created. The name can't be
changed, and it doesn't reveal the organization's subdomain. Don't confuse it
with the [login server]({{< relref "/docs/concepts/terminology.md#login-server" >}}).

## MagicDNS

With MagicDNS on (the default), every machine gets a DNS name:

- the short name, e.g. `my-server`, resolvable from other machines;
- the full name, e.g. `my-server.net3k9x2a.os.example.com`.

The name comes from the machine's name, which you can change with
**Rename** on **Machines**. MagicDNS is required for
[HTTPS certificates]({{< relref "https-certificates.md" >}}). See Tailscale's
[MagicDNS docs](https://tailscale.com/kb/1081/magicdns).

## Nameservers

The global nameservers machines use for other queries. New organizations get
`1.1.1.1`. Each entry must be an IP address or an `https://`
DNS-over-HTTPS URL.

ownscale sends them as *fallback* resolvers: machines keep using their local
DNS settings where they have them, and use these otherwise. ownscale can't
force all queries through the tailnet's nameservers.

## Split DNS

Split DNS sends queries for one domain, and its subdomains, to specific
nameservers. Use it to resolve private names such as `corp.example.com`
from an internal DNS server. Each domain needs at least one nameserver, and
a domain can't be inside the tailnet name.

## Search domains

Domains appended to short names. With `corp.example.com` as a search domain,
`ssh build01` tries `build01.corp.example.com`.

## DNS records

Extra `A`, `AAAA` or `CNAME` records served by MagicDNS to every machine,
for example `grafana.internal.example.com → 100.64.0.7`. Use them to give
services stable names that don't depend on machine names. Each name and
type pair must be unique.

## HTTPS certificates

Shows whether machines can get TLS certificates for their names. It needs
MagicDNS on and a DNS provider configured on the server. See
[HTTPS certificates]({{< relref "https-certificates.md" >}}).
