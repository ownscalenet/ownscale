---
title: HTTPS certificates
weight: 21
---

Traffic in a tailnet is already encrypted by WireGuard, but browsers and
many tools still expect `https://` with a trusted certificate. ownscale lets
machines get publicly trusted [Let's Encrypt](https://letsencrypt.org)
certificates for their MagicDNS names.

## Requirements

- The server administrator has set `DNS_PROVIDER` in `example.env`.
- [MagicDNS]({{< relref "dns.md#magicdns" >}}) is on for the organization.

The **DNS** page shows whether certificates are available. If they aren't,
it says why.

## Getting a certificate

On the machine:

```sh
sudo tailscale cert my-server.net3k9x2a.os.example.com
```

This writes `my-server.net3k9x2a.os.example.com.crt` and `.key` in the
current directory. Use them with any web server. Certificates last 90 days,
so rerun the command before they expire.

The easier option is `tailscale serve`, which gets and renews the certificate
automatically and proxies HTTPS to a local port:

```sh
sudo tailscale serve --bg 3000
```

See Tailscale's [`tailscale serve` docs](https://tailscale.com/kb/1312/serve).

## How it works

Let's Encrypt verifies a DNS-01 challenge: a TXT record in the machine's
domain. The Tailscale client asks the organization's Headscale instance,
which publishes the record through the server's DNS provider. The client
then gets the certificate directly from Let's Encrypt, so the private key
never leaves the machine.

## Things to know

- **Names are public.** Every certificate is recorded in public
  [Certificate Transparency](https://certificate.transparency.dev) logs, so
  machine names and tailnet names can be discovered. Don't put secrets in
  machine names.
- **Rate limit.** Let's Encrypt issues at most 50 new certificates per
  registered domain per week, and that limit is shared by every organization
  on the server.
- Certificates aren't available when `DNS_PROVIDER=none`, as in local
  development.

See Tailscale's [HTTPS docs](https://tailscale.com/kb/1153/enabling-https).
