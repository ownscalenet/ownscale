# ownscale e2e tests (Playwright)

Real, end-to-end browser tests against the actual `docker-compose.yml`
stack (`ownscale` + `dex` + `caddy`), backed by a real, freshly-migrated
SQLite database. Nothing here is mocked: OIDC login goes through a real
bundled Dex instance, organization creation spins up real headscale +
headplane Docker containers, and the access control policy is really
uploaded to and restarts a real headscale container.

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
   (real headscale + headplane containers) -- this is the slowest step,
   normally well under a minute.
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
  need an existing machine row.
- Headplane's own UI/OIDC-SSO-into-headplane is not tested.
- Not wired into CI; run locally/manually.
