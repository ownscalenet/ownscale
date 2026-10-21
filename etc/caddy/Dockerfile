# Built from the repository root (see compose.yaml) so the docs site in site/
# can be rendered here.

FROM caddy:2-builder AS builder

# DNS_PROVIDER env var switches the provider at runtime.
RUN xcaddy build \
	--with github.com/caddy-dns/azure@v0.6.0 \
	--with github.com/caddy-dns/cloudflare@v0.2.4 \
	--with github.com/caddy-dns/hetzner/v2@v2.0.1 \
	--with github.com/caddy-dns/rfc2136@v1.0.0

FROM ghcr.io/gohugoio/hugo:v0.165.0 AS site
COPY --chown=hugo:hugo site /project
# site/go.mod may need a newer Go than the image ships.
ENV GOTOOLCHAIN=auto
RUN hugo --source /project --destination /tmp/site --minify

FROM caddy:2

COPY --from=builder /usr/bin/caddy /usr/bin/caddy

COPY etc/caddy/Caddyfile /etc/caddy/Caddyfile
COPY etc/caddy/providers /etc/caddy/providers
COPY etc/caddy/content-domain-site /etc/caddy/content-domain-site
COPY --from=site /tmp/site /srv/site
