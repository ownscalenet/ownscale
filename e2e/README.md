# e2e/frontend

Playwright end-to-end tests for the React SPA (`frontend/`), driven through
a real browser against a real running ownscale stack -- no mocking of the
backend, Dex, Caddy, or headscale.

Kept as its own package (not part of `frontend/package.json`) so
Playwright and its browser binaries never weigh down the production
`make frontend` build.

## Prerequisites

1. `make dev-up` running (see the repository README's "Local development" /
   "Deployment" sections) with `BASE_DOMAIN=ownscale.test` (the default in
   `.env.example`), including the `/etc/hosts` entries it documents.
2. Node.js (matching `frontend/`'s toolchain).

These tests run against whatever is already in your local dev stack and
will create/modify real data there (pre-auth keys, invites, ACL policy
edits that are reverted at the end of the spec) -- mingling with your local
dev deployment is expected and fine.

## Running

```sh
make e2e-frontend
```

Equivalent to, run from `e2e/frontend/`:

```sh
npm ci
npx playwright install chromium
npm test
```

If Chromium fails to launch with missing shared-library errors (typical on
a bare/minimal Linux host that has never run a browser before), install
its OS dependencies once with `npx playwright install --with-deps chromium`
instead (requires apt and sudo).

Other useful commands (from `e2e/frontend/`):

- `npm run test:headed` -- watch the browser while tests run.
- `npm run test:ui` -- Playwright's interactive UI mode, good for
  debugging a single spec.
- `npm run report` -- open the HTML report from the last run.

## Notes

- `playwright.config.ts` points at `https://ownscale.test` by default;
  override with `E2E_BASE_URL` if your `.env` uses a different
  `BASE_DOMAIN`.
- `ignoreHTTPSErrors: true` is set instead of requiring every contributor
  to trust the local dev CA (see `make load-cert`).
- A `setup` project (`tests/global.setup.ts`) logs in once as the Dex
  seeded `admin@ownscale.test` user and caches the session in
  `tests/.auth/admin.json` (gitignored); every other spec starts already
  authenticated. Specs that need to exercise the login/logout flow itself
  (`login.spec.ts`, `onboarding.spec.ts`) opt out of that cached state.
- This suite is local-only for now (like `make test-integration`) and
  isn't wired into GitHub Actions CI.
