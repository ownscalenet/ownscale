---
title: Resolve internal domains
weight: 50
---

Split DNS sends queries for a domain, and its subdomains, to specific
nameservers instead of the global ones. Use it to resolve private names such
as `corp.example.com` from an internal DNS server.

1. On the [DNS](/console/dns) page, under **Split DNS**, add the domain and
   its nameservers. Nameservers can be IP addresses or `https://`
   DNS-over-HTTPS URLs.
2. Make sure devices can reach the nameserver: it's usually a device in the
   tailnet, or on a network behind a [subnet
   router]({{< relref "home-network.md" >}}).

Devices only use it when they accept DNS settings from the tailnet
(`tailscale set --accept-dns`, the default).
