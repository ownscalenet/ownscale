FROM caddy:2-builder AS builder

# DNS_PROVIDER env var switches the provider at runtime.
RUN xcaddy build \
	--with github.com/caddy-dns/azure@v0.6.0 \
	--with github.com/caddy-dns/cloudflare@v0.2.4 \
	--with github.com/caddy-dns/route53@v1.6.2 \
	--with github.com/caddy-dns/hetzner/v2@v2.0.1 \
	--with github.com/caddy-dns/digitalocean@04bde2867106

FROM caddy:2

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
