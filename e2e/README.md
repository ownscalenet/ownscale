# ownscale e2e tests (Playwright)

Real, end-to-end browser tests against the actual `docker-compose.yml`
stack (`ownscale` + `dex` + `caddy`), backed by a real, freshly-migrated
SQLite database. Nothing here is mocked: OIDC login goes through a real
bundled Dex instance, organization creation spins up a real headscale Docker container, and the
access control policy is really uploaded to a real headscale container via
its API.

## Prerequisites

- Docker running locally, and the repo's usual local-dev setup already in
  place: `config.yaml` and `.env` present (see the repo root `README.md`),
  the `ownscale:latest` image built (or let `--build` handle it, which
  takes longer).
- `/etc/hosts` entries for `ownscale.test`, `dex.ownscale.test`, and
  `acme.ownscale.test` (the fixed subdomain this suite provisions), all
  pointing at `127.0.0.1`. See the repo root `README.md`'s "Headscale
  organizations" section.
- Node.js (for Playwright itself).

## Running

```sh
npm --prefix e2e ci
npx --prefix e2e playwright install --with-deps chromium   # once
npm --prefix e2e test
```

Or, from the repo root: `make e2e` (does all three steps above).

The `setup` project (`tests/global.setup.ts`, runs automatically before
every spec file) will:

1. Remove any leftover per-organization Docker containers from a previous
   run, reset the local `data/ownscale.sqlite` database, and bring the
   core `docker-compose.yml` services up.
2. Apply migrations against the fresh database.
3. Log in as `admin@ownscale.test` (password `ownscale`, from
   `etc/dex/config.yaml`) through the real Dex OIDC flow.
4. Create and wait for the `acme` organization to finish provisioning
   (a real headscale container) -- this is the slowest step, normally
   well under a minute.
5. Save the authenticated session for reuse by the other spec files.

**This destroys the local `data/ownscale.sqlite` database and any existing
`acme` organization/containers on every run.** That's intentional (this is
a throwaway local dev environment), not something to point at anything
you care about.

## What's covered

- `auth.spec.ts` -- signs in via the upstream OIDC provider (Dex) from a
  fresh, unauthenticated context.
- `machines.spec.ts` -- Machines page rendering, filters, empty state.
- `users.spec.ts` -- Members list, creating/renaming/deleting a headscale
  user.
- `access-control.spec.ts` -- editing the ACL policy, the Format button,
  the View diff button/modal, saving, and persistence across a reload.
- `invite.spec.ts` -- creating an invite as the org owner and accepting it
  as a second, independently-logged-in user (`user1@ownscale.test`).

## Known limitations

- No real Tailscale client is registered against the e2e organization, so
  `machines.spec.ts` only covers the page's own rendering/filters/empty
  state, not per-machine actions (rename/tag/routes/expire/delete) which
  need an existing machine row. See `tailscale-connectivity.sh` below for
  a real-client test.
- Not wired into CI; run locally/manually.

## `tailscale-connectivity.sh`: real Tailscale client + DERP connectivity test

A standalone bash script (not part of the Playwright suite above) that
provisions a fresh, throwaway organization, drives the real invite HTTP
API to get an owner and an invitee both registered to it, then runs two
real Tailscale clients, each in its own network-isolated Docker
container, with no route to each other or to any other container in this
stack and verifies they can reach each other. Since the two client
containers share no network route, a successful ping is only possible via
headscale's coordination plus a DERP relay

### Prerequisites

- The `docker-compose.yml` at root is up and running
- `docker`, `curl`, `jq`, and `gnome-terminal` on `PATH`.
- `.env` present in the repo root with `BASE_DOMAIN` and
  `OWNSCALE_DATA_DIR` set (same file docker compose reads).
- No `/etc/hosts` entry is needed for the randomly-generated test
  subdomain: the orchestrating script only talks to the base domain's
  `/api/*`.

### Running

```sh
./e2e/tailscale-connectivity.sh
```

Set `KEEP_ORG=1` to skip deleting the test organization/users on exit.

### What it doesn't cover

- Interactive/browser-based OIDC login (see `auth.spec.ts` above for that).
- Direct (non-DERP) peer-to-peer connectivity -- the whole point of the
  isolated networks is to force a DERP relay path.
