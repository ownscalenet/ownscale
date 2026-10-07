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

All SQLite dbs are gathered under a single directory, `OWNSCALE_DATA_DIR`.

### Backups

ownscale continuously replicates its own database and every organization's
headscale database with [Litestream](https://litestream.io), embedded as a
library (see the `backup` package). Configure it via `.env` (see
`.env.example`): `OWNSCALE_BACKUP_REPLICA_URL` enables backups (e.g.
`s3://my-backup-bucket/ownscale`); `OWNSCALE_BACKUP_ACCESS_KEY_ID`/
`OWNSCALE_BACKUP_SECRET_ACCESS_KEY` (or an IAM role), `OWNSCALE_BACKUP_REGION`,
and `OWNSCALE_BACKUP_ENDPOINT` (for S3-compatible backends) are optional.
Leave `OWNSCALE_BACKUP_REPLICA_URL` empty to disable backups completely.

For local-only backups (no S3), point `OWNSCALE_BACKUP_REPLICA_URL` inside the
data directory, e.g. `file://<OWNSCALE_DATA_DIR>/.backups`: it's the only
persistent path the ownscale container mounts. Any other `file://` destination
only lives in the container's writable layer and gets wiped on every container
recreation. Such backups share a disk with the databases, so use S3 or Azure
in production.

On startup, `osa verify` fails if a database is missing although it has a
backup. Recover with `osa restore db` and `osa restore organizations`, run
while ownscale is stopped, e.g.
`docker compose run --rm ownscale /app/osa restore db`.


## Single sign-on (via ownscale's own OIDC provider)

Sign in at `https://console.<base_domain>/login` (ownscale — Ownscale console, login,
and OIDC provider — is bound to the `console` subdomain; the documentation
site lives at the bare `https://<base_domain>`). ownscale federates the initial
login to a configurable upstream OIDC provider (`OWNSCALE_AUTH_UPSTREAM_OIDC_*`) — any standard OIDC issuer works: Google, Okta, Dex, etc. —
then acts as its own OIDC provider so each organization's Headscale
instance trusts it as a relying party: one upstream login covers the
Ownscale console and the tailnet of the organization you're a member of.
See
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

Every organization gets its own headscale instance, running inside the
`ownscale` process and reachable at `<subdomain>.<base_domain>` (see
DECISIONS.md). Its database and keys live under
`${OWNSCALE_DATA_DIR}/<subdomain>/`.

Prerequisites:

- a wildcard DNS record `*.example.com` pointing at this server (for local
  dev, use `ownscale.test` instead -- see below -- which needs `/etc/hosts`
  entries per subdomain, since it has no real wildcard DNS)
- the `caddy` compose service running for HTTPS (see below)

`OWNSCALE_BASE_DOMAIN` must not be `"localhost"`: every OS and container resolves
`localhost` names from a built-in loopback entry before ever consulting DNS,
which silently defeats the `os-net` network aliases the `ownscale` container
relies on to reach Caddy for `console.` and `dex.` (`ownscale`/`osa` reject
`OWNSCALE_BASE_DOMAIN=localhost` outright).

For local dev, use a private, [RFC 2606](https://www.rfc-editor.org/rfc/rfc2606)-reserved
TLD like **`ownscale.test`** (the default in `.env.example`). It has no
real wildcard DNS, so every
subdomain you actually use locally needs an `/etc/hosts` entry pointing at
`127.0.0.1`: `ownscale.test`, `console.ownscale.test`, `dex.ownscale.test`,
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

### Wildcard TLS certificate (production, via DNS-01)

For organizations names and funnels we use separate subdomains. TLS is done with
a wildcard certificate, which needs a DNS-01 challenge. Funnels can live on a
separate domain (`FUNNEL_DOMAIN`, see `.env.example`), which gets its own
wildcard certificate from the same DNS provider. Caddy ships with plugins
for 5 providers (see `etc/caddy/providers/`); pick one with `DNS_PROVIDER` in
`.env` and set that provider's credential vars: `azure` (default), `cloudflare`,
`route53`, `hetzner`, `digitalocean`.

### Funnel limits

Funnels never wake a sleeping organization; requests get 503 until the
target machine reconnects. Each organization's funnels are limited to
100 req/s (burst 200) and 256 concurrent requests; bodies are capped by
`FUNNEL_MAX_BODY_BYTES` (32 MiB by default).

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

The `ownscale` service trusts this same local CA (via `SSL_CERT_DIR`), so it
and the headscale instances running inside it can reach their OIDC
providers (the console itself and, e.g., the bundled Dex instance) when
they're proxied through Caddy under a non-public `BASE_DOMAIN`. This only
matters for a non-public `BASE_DOMAIN` (e.g. `ownscale.test`), where Caddy
issues its own self-signed certs instead of publicly-trusted ones; it's a
no-op in production. Caddy has `console.{BASE_DOMAIN}` and
`dex.{BASE_DOMAIN}` as network aliases on `os-net` (see
`docker-compose.yml`), so `ownscale` resolves those names straight to Caddy.
