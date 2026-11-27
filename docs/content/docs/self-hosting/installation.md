---
title: Installation
weight: 20
aliases:
  - /docs/installation/
---

Ownscale ships as a Docker Compose stack:

- the `ownscale` container runs the console, the API and one Headscale
  instance per organization, all in a single process;
- the `ownscale` container also serves this documentation site, every
  organization's login server and every funnel;
- the `caddy` container handles TLS and forwards requests to `ownscale`. You
  can [use your own reverse proxy]({{< relref "byorp.md" >}}) too.

Check the [requirements]({{< relref "requirements.md" >}}) first.

## Install script

On the server, run:

```sh
curl -fsSL https://raw.githubusercontent.com/ownscalenet/ownscale/master/install.sh | sh
```

It asks for a directory, your domain and your reverse proxy, then:

- downloads the latest release's `compose.yaml`, `example.env` and
  `Makefile`;
- creates `.env` with your domain and a random session secret;
- for [your own reverse proxy]({{< relref "byorp.md" >}}), turns off the
  bundled Caddy and prints the config your proxy needs.

> [!IMPORTANT]
> The [example.env](https://github.com/ownscalenet/ownscale/blob/master/example.env)
> file is the reference documentation for all possible settings when
> self-hosting Ownscale.

Then start Ownscale as the script shows, and [sign in](#sign-in).
The `Makefile` is the usual way to interact with the Ownscale deployment.
Run `make help` to see a list of available commands.

## Sign in

Start the stack with `make up` and open `https://console.<BASE_DOMAIN>`.
The first sign-in creates your organization, and you're its owner. Invite
others from [Invitations](/console/invitations).

By default anyone who can reach the server can sign up and create their own
organization. For a private server, set `OWNSCALE_MAX_ORGS=1` in `.env`
before starting: once your organization exists, only invited users can sign
in.

## Upgrading

With the Makefile, run:

```sh
make upgrade TAG=v0.2.0
```

It downloads the release's `compose.yaml`, `example.env` and `Makefile`, sets
`OWNSCALE_VERSION` in `.env`, then pulls and restarts the stack. Compare
`example.env` with your `.env` for new settings.

## Next steps

- [Limitations]({{< relref "/docs/reference/limitations.md" >}}): what
  Ownscale doesn't support yet.
