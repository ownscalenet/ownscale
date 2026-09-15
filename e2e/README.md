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
3. Go and the repository's Go dependencies, plus an `/etc/hosts` entry for
   `aaaa.<base-domain>` (e.g. `127.0.0.1 aaaa.ownscale.test`) -- required
   only by `machines-lifecycle.spec.ts` and `funnel.spec.ts`, which build
   and launch `bin/tscl-bridge` (the same tsnet-backed real-device client
   used by the connectivity suite below) to register one real, ephemeral
   device per test. See "Browser-driven connectivity" below for the full
   host-entry list if you're already set up for `make e2e-connectivity`.

These tests run against whatever is already in your local dev stack and
will create/modify real data there (pre-auth keys, invites, ACL policy
edits that are reverted at the end of the spec, real ephemeral devices
that are deregistered at the end of their spec) -- mingling with your
local dev deployment is expected and fine.

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

- `playwright.config.ts` points at `https://console.ownscale.test` by
  default; override with `E2E_BASE_URL` if your `.env` uses a different
  `BASE_DOMAIN`.
- `ignoreHTTPSErrors: true` is set instead of requiring every contributor
  to trust the local dev CA (see `make load-cert`).
- A `setup` project (`tests/global.setup.ts`) logs in once as the Dex
  seeded `admin@ownscale.test` user and caches the session in
  `tests/.auth/admin.json` (gitignored); every other spec starts already
  authenticated. Specs that need to exercise the login/logout flow itself
  (`login.spec.ts`, `onboarding.spec.ts`) opt out of that cached state.
  That same setup also self-registers a fixed `passkey@ownscale.test`
  identity via passkey, claiming seed org `dddd`, and caches its WebAuthn
  credential in `tests/.auth/passkey-credential.json` (gitignored) so
  `passkey.spec.ts` can sign back in as that identity without registering
  another brand-new user/org.
- This suite is local-only for now (like `make test-integration`) and
  isn't wired into GitHub Actions CI.

## Browser-driven connectivity

Run this separately from the existing frontend suite:

```sh
make e2e-connectivity
# Equivalent, after the one-time npm ci / Chromium installation above:
npm --prefix e2e/frontend run test:connectivity
```

The connectivity config selects only `tests/connectivity/connectivity.spec.ts`.
It uses one worker, no retries, a 30-minute test budget (including up to
17 minutes of fixture readiness), and **no automatic traces, screenshots,
or videos**. It does not use the default suite's cached admin session.
The default frontend config excludes the entire connectivity directory.
Go and the repository's Go dependencies must be available; global setup
builds `bin/tscl-bridge` once, then each test launches that compiled binary.
Build/browser scratch files remain under ignored `bin/connectivity-work`.

### Live prerequisites

- Start the stack yourself with `make dev-up`. Tests do not start, restart,
  recreate, reset, or inspect the stack database.
- Use the seeded Dex admin, user1, and user2 identities. Their organizations
  must be `aaaa`, `bbbb`, and `cccc`, respectively, with owner/manager
  permissions. They log in sequentially in that order. A different existing
  assignment fails with an actionable prerequisite error; tests never
  reassign users. Each organization has up to five minutes to become ready.
- The existing local CA certificate must be readable. The Go child receives
  `SSL_CERT_FILE` and verifies TLS; it never disables certificate checking.
  Preflight also validates HTTPS for the app and Dex discovery endpoint.
- The `web2` funnel label in `aaaa` must be unused. Existing routes and
  unresolved recovery artifacts block the run rather than being overwritten.
- Ensure these host entries already exist (tests only read `/etc/hosts`):

  ```text
  127.0.0.1 ownscale.test console.ownscale.test dex.ownscale.test
  127.0.0.1 aaaa.ownscale.test bbbb.ownscale.test cccc.ownscale.test
  127.0.0.1 web1-aaaa.ownscale.test web2-aaaa.ownscale.test
  127.0.0.1 web1-bbbb.ownscale.test web2-bbbb.ownscale.test
  127.0.0.1 web1-cccc.ownscale.test web2-cccc.ownscale.test
  ```

  Substitute your configured domain if different. Wildcard DNS is not needed.

Environment overrides (export before invoking Make/npm):

| Variable | Default | Purpose |
| --- | --- | --- |
| `E2E_BASE_DOMAIN` | `ownscale.test` | Organization/funnel hostname suffix |
| `E2E_BASE_URL` | `https://console.${E2E_BASE_DOMAIN}` | Existing application endpoint |
| `E2E_DEX_URL` | `https://dex.<base-domain>` | Existing Dex endpoint |
| `E2E_CA_FILE` | `data/local-ca/root.crt` | Existing CA; relative paths resolve from repository root |
| `OWNSCALE_DATA_DIR` | `data` | Alternative data directory for the default CA path |
| `E2E_ENROLLMENT_TIMEOUT_MS` | `90000` | Node startup deadline; integer 1000–300000 ms |

Shell variables are used; the runner does not source `.env`. If your stack
uses a nondefault data directory/domain, export the corresponding values.
Individual probes default to five seconds; RPC deadlines are longer so a
network timeout is a probe result, not a harness timeout.

### Exclusivity, cleanup, and recovery

Both live frontend configs acquire `e2e/.frontend-run.lock` exclusively,
outside Playwright's output directories. `--list` does not acquire it.
An existing lock is never automatically discarded, even if its PID appears
dead. Do not run the suites concurrently or change the same organizations
manually during a run.

Normal teardown restores captured ACLs, removes only this run's routes and
machines, expires its keys, and shuts down its nodes. Cleanup errors remain
test failures. An unresponsive bridge is terminated by its exact PID, first
with SIGTERM and then SIGKILL; forced termination is explicitly reported
as a cleanup failure.

Private recovery records are retained under ignored
`e2e/.connectivity-private/` (mode 0700). After an interrupted run:

1. Stop competing frontend runners and inspect the guard PID locally;
   ensure the runner and its owned bridge are no longer active.
2. Inspect private recovery records locally. Through the authenticated UI,
   restore each recorded original ACL and wait for headscale readiness.
   Remove only recorded owned routes/machines and expire owned keys.
3. Resolve/remove the corresponding recovery record only after verifying
   restoration and cleanup. Remove a stale guard manually only once no
   run remains and recovery has been accounted for.

Never attach recovery records, auth keys, cookies, passwords, raw bridge
stdin/stderr, or copied authenticated session state to reports or issues.
Bridge diagnostics expose only process/lifecycle metadata and counters;
stderr text is drained without retaining or publishing it.

### Offline contract checks

No running stack, hosts entries, CA, browser, or synthetic login is needed:

```sh
npm --prefix e2e/frontend run test:connectivity:contract
cd e2e/frontend && npx tsc --noEmit
```

These use the existing Playwright runner and build the Go adapter once.
They exercise actual binary negotiation/invalid commands, partial and
malformed IPC, runtime validation, deadlines, process exits, secret-safe
diagnostics, forced cleanup, and exclusive guard ownership.
