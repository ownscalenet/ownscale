# Built from the repository root (see compose.dev.yaml).

FROM caddy:2-builder AS builder

# DNS_PROVIDER env var switches the provider at runtime. caddy-ratelimit has
# one tag, which is old, so it's pinned to a commit. libdns/azure is replaced
# with a fork that keeps the other values of a record set
# (https://github.com/libdns/azure/pull/12), so the apex and wildcard DNS-01
# challenges don't fail each other.
RUN xcaddy build \
	--with github.com/caddy-dns/azure@v0.6.0 \
	--with github.com/libdns/azure=github.com/gabriel-vasile/libdns-azure@25c64152411e040baf14d25442b495bac3df4f1d \
	--with github.com/caddy-dns/cloudflare@v0.2.4 \
	--with github.com/caddy-dns/hetzner/v2@v2.0.1 \
	--with github.com/caddy-dns/rfc2136@v1.0.0 \
	--with github.com/mholt/caddy-ratelimit@5625512f24f6f59d6f64fb3aafe5eecff0b286db

FROM caddy:2

COPY --from=builder /usr/bin/caddy /usr/bin/caddy

COPY etc/caddy/Caddyfile /etc/caddy/Caddyfile
COPY etc/caddy/providers /etc/caddy/providers
