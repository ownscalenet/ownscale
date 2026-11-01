---
title: Architecture
weight: 4
aliases:
  - /docs/architecture/
---

An ownscale deployment is two containers: `ownscale` and `caddy`.

```mermaid
flowchart TB
  Internet((Internet)) --> Caddy
  subgraph host
    Caddy[caddy: TLS, routing, docs site]
    subgraph ownscale process
      Console[console + API]
      OIDC[OIDC provider]
      HS1[Headscale: org abcd]
      HS2[Headscale: org efgh]
      Funnel[funnel proxy]
      Backup[Litestream]
    end
    DB[(SQLite databases)]
  end
  Caddy -->|console.BASE_DOMAIN| Console
  Caddy -->|abcd.BASE_DOMAIN| HS1
  Caddy -->|efgh.BASE_DOMAIN| HS2
  Caddy -->|label-abcd.CONTENT_DOMAIN| Funnel
  ownscale process --- DB
  Backup --> Remote[(S3 / Azure / file)]
```

## Caddy

Caddy terminates TLS for every hostname with wildcard certificates obtained
through a DNS-01 challenge, and routes by hostname:

- `<BASE_DOMAIN>`: this documentation site, built into the Caddy image;
- `console.<BASE_DOMAIN>`: the web console and its API;
- `<org>.<BASE_DOMAIN>`: the organization's Headscale instance;
- `<label>-<org>.<CONTENT_DOMAIN>`: a funnel.

See [Domains and URLs]({{< relref "/docs/reference/domains-and-urls.md" >}}).

## One process, one Headscale per organization

Every organization's Headscale runs inside the ownscale process, from a fork
at [github.com/ownscalenet/headscale](https://github.com/ownscalenet/headscale).
Each instance listens on a loopback port that ownscale forwards
`<org>.<BASE_DOMAIN>` traffic to, and ownscale manages it through a unix
socket, so no Headscale API keys exist. Running in-process keeps memory and
IO low enough to host many organizations on a small server. Idle instances
are stopped after 30 minutes and restarted on demand.

The DERP map is fetched by ownscale every 3 hours and pushed to every
instance. Headscale's embedded DERP server isn't used, because every
instance would need its own public port. Set `OWNSCALE_DERP_MAP_URL` to use
[your own relays](https://tailscale.com/kb/1118/custom-derp-servers).

## Login

ownscale is an OpenID Connect provider that every organization's Headscale
trusts. When a Tailscale client signs in, Headscale redirects to ownscale,
which signs the user in with the configured upstream OIDC provider, Google,
Microsoft, GitHub or a passkey, then sends them back.

## Funnels and the agent machine

A [funnel]({{< relref "/docs/features/funnels.md" >}}) needs a machine
inside the tailnet that can reach the target. When an organization's first
funnel is created, ownscale starts an embedded Tailscale client (`tsnet`)
that joins the tailnet as the `ownscale-agent` machine, owned by an
`ownscale-agent` user. Public requests arrive at Caddy, go to ownscale's
funnel proxy and are forwarded through that client to the target machine.

Funnels are experimental and will change.

## Database and backups

ownscale stores everything in SQLite: one database for ownscale itself
(users, organizations, invites, funnels, settings) and one per organization
for its Headscale instance. All of them live under `OWNSCALE_DATA_DIR`.

[Litestream](https://litestream.io) runs inside the process and continuously
replicates every database to S3, an S3-compatible service, Azure Blob
Storage or a local directory. Replication runs on an interval
(`OWNSCALE_BACKUP_SYNC_INTERVAL`, 10 s by default), so if the server dies,
the most recent changes may not have been
[replicated yet](https://litestream.io/tips/#data-loss-window). This
trade-off gives a fast database with no cluster to manage. See
[`osa restore`]({{< relref "/docs/reference/osa-cli.md#restore" >}}).
