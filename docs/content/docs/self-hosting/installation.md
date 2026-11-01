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
gh auth login  # an account with access to the private ownscale repository
mkdir ownscale && cd ownscale
TAG=v0.1.0     # a release whose deployment assets were published successfully
gh release download "$TAG" --repo ownscalenet/ownscale \
  --pattern compose.yaml --pattern example.env
```

The release's `compose.yaml` pins both images to that version, so no source
checkout is needed. Assets appear after the release deployment workflow
succeeds, so a newly published release may still be building. Latest-release
shortcuts skip prereleases.

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
| `DNS_PROVIDER` | `azure`, `cloudflare`, `hetzner` or `rfc2136`, plus its credentials |

Then add at least one login provider, if you want more
than passkeys.

### Log in to the registry

Both GHCR packages are private. Use a token with `read:packages` and access
to `ownscale` and `ownscale-caddy`, authorized for organization SSO if
required:

```sh
printf '%s' "$GHCR_READ_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

Don't put tokens in `.env` or in shell command arguments.

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

## Installing from source

Clone the repository, copy `example.env` to `.env`, configure it, then run:

```sh
docker compose up -d --build
```

## Next steps

- [Quickstart]({{< relref "/docs/get-started/quickstart.md" >}}): create
  your organization and add the first machines.
- [Limitations]({{< relref "/docs/reference/limitations.md" >}}): what
  ownscale doesn't support yet.
