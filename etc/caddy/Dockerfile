# Built from the repository root (see compose.dev.yaml).

FROM caddy:2-builder AS builder

# DNS_PROVIDER env var switches the provider at runtime.
RUN xcaddy build \
	--with github.com/caddy-dns/azure@v0.6.0 \
	--with github.com/caddy-dns/cloudflare@v0.2.4 \
	--with github.com/caddy-dns/hetzner/v2@v2.0.1 \
	--with github.com/caddy-dns/rfc2136@v1.0.0

FROM caddy:2

COPY --from=builder /usr/bin/caddy /usr/bin/caddy

COPY etc/caddy/Caddyfile /etc/caddy/Caddyfile
COPY etc/caddy/providers /etc/caddy/providers
