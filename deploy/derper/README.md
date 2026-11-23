# Self-hosted DERP relay for ownscale

[DERP](https://tailscale.com/kb/1232/derp-servers) is a fallback relay
Tailscale/headscale nodes use when a direct peer-to-peer connection can't be
established. By default, organizations use Tailscale's public DERP servers.
This directory lets you run your own, which organizations can then add to
the relays they use.

## Why a separate server?

Tailscale's own `cmd/derper` docs explicitly warn against putting `derper`
behind an HTTP reverse proxy: DERP does a protocol switch inside TLS from
HTTP to a custom binary protocol that most proxies (including Caddy, used
for every other ownscale-managed service) break. `derper` therefore needs to
own ports 80/443 (for its own Let's Encrypt cert) and UDP 3478 (STUN)
directly, rather than sitting behind the same Caddy container that fronts
`ownscale`.

## Setup

1. Provision a small server with a public IPv4 (and ideally IPv6) address.
2. Point a DNS `A`/`AAAA` record for your chosen hostname (e.g.
   `derp.example.com`) at that server.
3. Open firewall/security-group rules for `tcp/80`, `tcp/443`, and `udp/3478`.
4. `cp .env.example .env` and set `DERP_HOSTNAME` (and optionally
   `DERP_ACME_EMAIL` and `DERP_LISTEN_IP`, if you want to bind the published
   ports to a specific interface instead of all interfaces).
5. `docker compose up -d --build`.
6. Verify: `curl -v https://<DERP_HOSTNAME>/` should return derper's status
   page, and Let's Encrypt should have issued a cert automatically (check
   `docker compose logs derper` on first start).

## Wiring it into ownscale

Once running, an owner or admin adds it under **Relays (DERP)** on the
console's DNS page: its hostname, and optionally its IPv4 and IPv6
addresses. The change applies right away, without restarting the
organization.
