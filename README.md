### Local development

```sh
cp config.example.yaml config.yaml   # edit base_domain, auth.*, database, etc.
make build              # builds frontend/ into internal/webui/dist, then `go build`
./bin/ownscale --config config.yaml   # start server on :8080
```

### Deployment (docker compose)

```sh
cp config.example.yaml config.yaml   # edit base_domain, auth.*, database, etc.
echo "BASE_DOMAIN=example.com" > .env
docker compose up -d --build   # builds and starts ownscale + caddy
docker compose exec ownscale /app/osa -config /etc/ownscale/config.yaml organization list
```

`make archive`  builds the `ownscale:latest` image locally, ships it to the
remote host via `docker save | ssh ... docker load`, then syncs the rest of
the repo over the same SSH connection. Afterwards, `docker compose up -d`

`docker-compose.yml` builds `ownscale` from the repo's `Dockerfile` (tagged
`ownscale:latest`, matching the service's `image:`) and runs it as the
`ownscale` service on the shared `os-net` Docker network, with **no host
port published** (Caddy reaches it at `ownscale:8080` by container name;

Config is loaded from `/etc/ownscale/config.yaml` by default, or from the path given
with `--config <path>`. See `config.example.yaml`. Any value can be overridden with an
`OWNSCALE_*` environment variable (e.g. `OWNSCALE_ADDR`)

### Data directory (`data_dir` / `OWNSCALE_DATA_DIR`)

All SQLite dbs are gathered under a single directory, `data_dir`. This allows us
to have all dbs replicated with a litestream directory watcher.


Configure it via `.env` (see `.env.example`): `LITESTREAM_REPLICA_URL` is
required (e.g. `s3://my-backup-bucket/ownscale`); `LITESTREAM_ACCESS_KEY_ID`/
`LITESTREAM_SECRET_ACCESS_KEY` (or an IAM role), `LITESTREAM_REGION`, and
`LITESTREAM_ENDPOINT` (for S3-compatible backends) are optional. The
actual litestream config lives in `etc/litestream/litestream.yml`, which
follows the same "static file + env var expansion" pattern as
`etc/caddy/Caddyfile` — it's not generated from `config.yaml`.


## Headscale organizations (per-subdomain)

Provision one headscale container, plus a [Headplane](https://github.com/tale/headplane)
web UI container, per subdomain of `base_domain` using the Docker SDK.

Prerequisites:

- a wildcard DNS record `*.example.com` pointing at this server
- Docker running locally (the standard `DOCKER_HOST` env vars are honored)
- the `caddy` compose service running for HTTPS (see below)

## Trust Caddy localhost certs
1. Extract the root cert from the running container:
docker cp <caddy-container>:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt
2. Import into Firefox (Firefox uses its own NSS store, ignoring the OS trust store by default):
• Go to  about:preferences#privacy  → scroll to Certificates → View Certificates
• Authorities tab → Import → select  caddy-root.crt
• Check "Trust this CA to identify websites"
