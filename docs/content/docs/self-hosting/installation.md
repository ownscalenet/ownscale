---
title: Installation
weight: 2
aliases:
  - /docs/installation/
---

ownscale ships as a Docker Compose stack:

- the `ownscale` container runs the console, the API and one Headscale
  instance per organization, all in a single process;
- the `caddy` container handles TLS and routes requests to the console, this
  documentation site, every organization's login server and every funnel.

Check the [requirements]({{< relref "requirements.md" >}}) first.

## Quick start

{{% steps %}}

### Download the release files

```sh
mkdir ownscale && cd ownscale
TAG=v0.1.0     # pick a release from github.com/ownscalenet/ownscale/releases
for f in compose.yaml example.env; do
  curl -fsSLO "https://raw.githubusercontent.com/ownscalenet/ownscale/$TAG/$f"
done
```

### Configure

```sh
cp example.env .env
```

Edit `.env`. At minimum, set:

| Setting | Value |
| --- | --- |
| `BASE_DOMAIN` | your wildcard domain, e.g. `example.com` |
| `CONTENT_DOMAIN` | optional separate domain for user content |
| `OWNSCALE_AUTH_SESSION_SECRET` | a long random string, e.g. from `openssl rand -hex 32` |
| `OWNSCALE_VERSION` | the release you downloaded, e.g. `v0.1.0` |
| `DNS_PROVIDER` | `azure`, `cloudflare`, `hetzner` or `rfc2136`, plus its credentials |

Then add at least one login provider, if you want more
than passkeys.

### Start

```sh
docker compose pull
docker compose up -d
```

On first start, Caddy requests the wildcard certificates, which can take a
minute or two. Follow progress with `docker compose logs -f caddy`.

### Sign in

Open `https://console.<BASE_DOMAIN>/login` and sign in. Your first sign-in
creates your organization, and you become its owner.

{{% /steps %}}

## Upgrading

Download `compose.yaml` and `example.env` for the new release as above,
compare `example.env` with your `.env` for new settings, set
`OWNSCALE_VERSION` to the new release, then run:

```sh
docker compose pull
docker compose up -d
```

## Installing from source

Clone the repository, copy `example.env` to `.env`, configure it, then build
the images and run them:

```sh
docker build -t ghcr.io/ownscalenet/ownscale:dev .
docker build -t ghcr.io/ownscalenet/ownscale-caddy:dev -f etc/caddy/Dockerfile .
OWNSCALE_VERSION=dev docker compose up -d
```

## Next steps

- [Limitations]({{< relref "/docs/reference/limitations.md" >}}): what
  ownscale doesn't support yet.
