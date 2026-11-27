---
title: How Ownscale works
weight: 50
---

Ownscale runs as two containers:

- **Caddy** terminates TLS on ports 80 and 443 and forwards every request to
  Ownscale. You can use [your own reverse
  proxy]({{< relref "/docs/self-hosting/byorp.md" >}}) instead.
- **Ownscale** is a single process that serves:
  - the console and its API, at `console.<BASE_DOMAIN>`;
  - one Headscale instance per organization, at `<org>.<BASE_DOMAIN>`,
    which Tailscale clients use as their login server;
  - funnels, at `<name>-<org>.<CONTENT_DOMAIN>` (the
    [content domain](/docs/self-hosting/content-domain/));
  - this documentation, at `<BASE_DOMAIN>`.

Traffic between devices never goes through Ownscale: they talk WireGuard to
each other directly.

## Data

Everything lives in SQLite databases under `OWNSCALE_DATA_DIR`: one for
Ownscale, one per organization. With `OWNSCALE_BACKUP_*` set, each is
replicated continuously to S3, Azure Blob Storage or a local path. See
[`osa restore`]({{< relref "/docs/reference/osa-cli.md#restore" >}}).
