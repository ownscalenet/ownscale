### Local development

```sh
cp .env.example .env   # edit BASE_DOMAIN, OWNSCALE_AUTH_*, etc.
make build              # builds frontend/ into webui/dist, then `go build`
set -a; source .env; set +a
./bin/ownscale   # start server on :8080
```

### Deployment (docker compose)

```sh
cp .env.example .env   # edit BASE_DOMAIN, OWNSCALE_AUTH_*, etc.
docker compose up -d --build   # builds and starts ownscale + caddy
docker compose exec ownscale /app/osa organization list
```

### Frontend end-to-end tests

`make e2e-frontend` runs a Playwright suite against a real, already-running
`make dev-up` stack (real HTTPS, Dex, and headscale -- no mocking). See
`e2e/README.md`.

`make archive`  builds the `ownscale:latest` image locally, ships it to the
remote host via `docker save | ssh ... docker load`, then syncs the rest of
the repo over the same SSH connection. Afterwards, `docker compose up -d`

`docker-compose.yml` builds `ownscale` from the repo's `Dockerfile` (tagged
`ownscale:latest`, matching the service's `image:`) and runs it as the
`ownscale` service on the shared `os-net` Docker network, with **no host
port published** (Caddy reaches it at `ownscale:8080` by container name;

Config is loaded from built-in defaults, overridden by any `OWNSCALE_*`
environment variable (e.g. `OWNSCALE_ADDR`, `OWNSCALE_BASE_DOMAIN`). See
`.env.example` for the full list.

### Data directory (`OWNSCALE_DATA_DIR`)

All SQLite dbs are gathered under a single directory, `OWNSCALE_DATA_DIR`. This allows us
to have all dbs replicated with a litestream directory watcher.


Configure it via `.env` (see `.env.example`): `LITESTREAM_REPLICA_URL` is
required (e.g. `s3://my-backup-bucket/ownscale`); `LITESTREAM_ACCESS_KEY_ID`/
`LITESTREAM_SECRET_ACCESS_KEY` (or an IAM role), `LITESTREAM_REGION`, and
`LITESTREAM_ENDPOINT` (for S3-compatible backends) are optional. The
actual litestream config lives in `etc/litestream/litestream.yml`, which
follows the same "static file + env var expansion" pattern as
`etc/caddy/Caddyfile` — it's not generated from ownscale's own config.

For local-only backups (no S3), set `LITESTREAM_REPLICA_URL` to
`file:///var/lib/litestream/backups` — the only `file://` path docker-compose.yml
persistently mounts for the litestream service (to
`${OWNSCALE_DATA_DIR}/.litestream-backups` on the host). Any other `file://`
destination only lives in the container's writable layer, gets wiped on every
container recreation, and desyncs litestream's local retention from the
(now-empty) destination, causing a permanent "shutdown sync failed" retry loop
on restart.


## Single sign-on (via ownscale's own OIDC provider)

Sign in at `https://<base_domain>/login` (ownscale — Ownscale console, login,
and OIDC provider — is bound to the bare base domain; the documentation
site lives at `https://docs.<base_domain>`). ownscale federates the initial
login to a configurable upstream OIDC provider (`OWNSCALE_AUTH_UPSTREAM_OIDC_*`) — any standard OIDC issuer works: Google, Okta, Dex, etc. —
then acts as its own OIDC provider so each organization's Headscale
container trusts it as a relying party: one upstream login covers the
Ownscale console and every organization's tailnet. See
`site/content/docs/getting-started.md` for details.

For local dev, `docker-compose.yml` bundles a
[Dex](https://dexidp.io/) instance (see the `dex` service and
`etc/dex/config.yaml`) as a lightweight, zero-external-account-setup
upstream provider — `.env.example`'s `OWNSCALE_AUTH_UPSTREAM_OIDC_*` defaults point
at it already. It seeds one test login: `admin@ownscale.test` /
`ownscale`. Add more entries to `etc/dex/config.yaml`'s `staticPasswords`
to test with additional users. In production, point `OWNSCALE_AUTH_UPSTREAM_OIDC_*`
at a real IdP instead and simply omit the `dex` service — ownscale's own
code never references Dex specifically, only generic OIDC discovery.

## Headscale organizations (per-subdomain)

Provision one headscale container per subdomain of `base_domain` using the
Docker SDK.

Prerequisites:

- a wildcard DNS record `*.example.com` pointing at this server (for local
  dev, use `ownscale.test` instead -- see below -- which needs `/etc/hosts`
  entries per subdomain, since it has no real wildcard DNS)
- Docker running locally (the standard `DOCKER_HOST` env vars are honored)
- the `caddy` compose service running for HTTPS (see below)

`OWNSCALE_BASE_DOMAIN` must not be `"localhost"`: every OS and container resolves
that exact name from a built-in loopback entry (`/etc/hosts`'s
`127.0.0.1 localhost`) before ever consulting DNS, which silently defeats
the Docker-network-alias routing organization containers rely on to reach
Caddy (`ownscale`/`osa` reject `OWNSCALE_BASE_DOMAIN=localhost` outright).

For local dev, use a private, [RFC 2606](https://www.rfc-editor.org/rfc/rfc2606)-reserved
TLD like **`ownscale.test`** (the default in `.env.example`):
since ownscale's own upstream OIDC login no longer has to be a real,
publicly-trusted IdP (see "Single sign-on" above), there's nothing left
requiring a real public domain like the previously-used `lvh.me`. Unlike
`lvh.me`, though, `ownscale.test` has no real wildcard DNS, so every
subdomain you actually use locally needs an `/etc/hosts` entry pointing at
`127.0.0.1`: `ownscale.test`, `dex.ownscale.test`, `docs.ownscale.test`,
and one per organization created (e.g. `acme.ownscale.test`). A
`dnsmasq`-based wildcard resolver is a nicer alternative if you create
organizations often.

You still won't get real certificates for a domain like this: Let's
Encrypt rejects reserved/non-public TLDs outright (`Cannot issue for
"os.test": Domain name does not end with a valid public suffix (TLD)`).
Set `CADDY_LOCAL_CERTS=local_certs` in `.env` (see `.env.example`) so Caddy
issues everything -- including the on-demand per-organization certs, and
Dex's own certificate -- from its own internal, self-signed CA instead of
ACME. Leave it unset in production, where `base_domain` is a real,
reachable public domain and Caddy should get publicly-trusted certificates.

Setting `CADDY_LOCAL_CERTS` also changes how organization creation behaves:
ownscale then copies Caddy's local CA into every new headscale container and
requires that copy to succeed, failing organization creation immediately if
it doesn't, rather than silently starting a container that would later fail
OIDC login with a confusing TLS trust error. When unset (production), that
copy step is skipped entirely. It similarly changes how ownscale itself
trusts its own configured `auth.upstream_oidc.issuer` (e.g. the bundled Dex
instance): see `auth.RegisterUpstreamOIDC`.

### Wildcard TLS certificate (production, via Azure DNS-01)

For organizations names and funnels we use separate subdomains. TLS is done with
a wildcard certificate. That needs API support and a caddy plugin. Because ownscale
is deployed on azure, only azure is supported now.

## Trust Caddy local dev certs

The pre-generated root CA cert lives at `${OWNSCALE_DATA_DIR}/local-ca/root.crt`
on the host (see `localca` and docker-compose.yml's `local-ca-init`
service) -- no need to `docker cp` it out of a running container.

### OS trust store (needed for CLI tools, e.g. `tailscale up`)

Tools that use the OS/Go trust store (like the `tailscale` CLI, or `curl`
without `-k`) won't trust an organization's `https://<subdomain>.<base_domain>`
endpoint until this CA is installed system-wide. On Debian/Ubuntu:

```bash
sudo cp "$OWNSCALE_DATA_DIR/local-ca/root.crt" /usr/local/share/ca-certificates/ownscale-local-ca.crt
sudo update-ca-certificates
```

### Firefox (uses its own NSS store, ignoring the OS trust store by default)

1. Go to `about:preferences#privacy` → scroll to Certificates → View Certificates
2. Authorities tab → Import → select `${OWNSCALE_DATA_DIR}/local-ca/root.crt`
3. Check "Trust this CA to identify websites"

Organization headscale containers trust this same local CA automatically:
they mount the `caddy-data` volume read-only and, if Caddy's local CA is
present there, headscale gets it appended to its system CA bundle. The
`ownscale` service itself does the same (see `auth.go`), so it can
trust its own upstream OIDC provider (e.g. the bundled Dex instance) when
that's also proxied through Caddy under a non-public `BASE_DOMAIN`. This
only matters for a non-public `BASE_DOMAIN` (e.g. `ownscale.test`), where
Caddy issues its own self-signed certs instead of publicly-trusted ones;
it's a no-op in production. Additionally, dockerctl connects the running
Caddy container to each organization's Docker network with `BASE_DOMAIN` as
an alias, so those containers can resolve `https://{BASE_DOMAIN}` straight
to Caddy for OIDC discovery.
