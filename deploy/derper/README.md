# Self-hosted DERP relay for ownscale

[DERP](https://tailscale.com/kb/1232/derp-servers) is a fallback relay
Tailscale/headscale nodes use when a direct peer-to-peer connection can't be
established. By default, every organization's headscale instance uses Tailscale's own public DERP relay map. This
directory lets you run your own instead, and have every organization's
headscale point at it (see `OWNSCALE_DERP_*` in `.env.example`).

## Why a separate server?

Tailscale's own `cmd/derper` docs explicitly warn against putting `derper`
behind an HTTP reverse proxy: DERP does a protocol switch inside TLS from
HTTP to a custom binary protocol that most proxies (including Caddy, used
for every other ownscale-managed service) break. `derper` therefore needs to
own ports 80/443 (for its own Let's Encrypt cert) and UDP 3478 (STUN)
directly, rather than sitting behind the same Caddy container that fronts
`ownscale`, `dex`, and every organization's headscale containers.

The simplest way to satisfy that without any port juggling on the main
ownscale host is to run `derper` on its own separate server, with its own
public DNS name.

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

Once running, set the `OWNSCALE_DERP_*` env vars (see
`.env.example`) to this server's hostname/region info. On next
organization provisioning (or reconciliation), every org's headscale
container will be configured with a custom DERP map pointing at this relay
instead of Tailscale's public one.
