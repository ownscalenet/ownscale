# e2e/frontend

Playwright end-to-end tests for the React SPA (`frontend/`), driven through
a real browser against a real running ownscale stack -- no mocking of the
backend, Dex, Caddy, or headscale.

Kept as its own package (not part of `frontend/package.json`) so
Playwright and its browser binaries never weigh down the production
`make frontend` build.

## Prerequisites

1. `make dev-up` running. It needs no `.env` setup: `compose.dev.yaml` sets
   the dev domain, `ownscale.test`. Map its names in `/etc/hosts`:
   `127.0.0.1 ownscale.test console.ownscale.test dex.ownscale.test` plus
   `aaaa.ownscale.test`, `bbbb.ownscale.test` and so on for the
   organizations.
2. Node.js (matching `frontend/`'s toolchain).
3. Go and the repository's Go dependencies, plus `/etc/hosts` entries for
   the e2e accounts' organizations, which tests that register real devices
   (via `bin/tscl-bridge` or devbox containers) reach by name. On a fresh
   stack those are:

   ```text
   127.0.0.1 eeee.ownscale.test ffff.ownscale.test gggg.ownscale.test hhhh.ownscale.test
   ```

   The setup project fails with the exact missing line if one doesn't
   resolve. See "Browser-driven connectivity" below for the connectivity
   suite's own entries.

## Test accounts and parallel runs

Each suite, and each frontend worker, runs as its own Dex identity that owns
its own organization, so they never change each other's policy, DNS,
invites or members (`tests/support/account.ts`):

| Who | Identity | Organization (fresh stack) |
| --- | --- | --- |
| connectivity suite | admin, user1, user2 | aaaa, bbbb, cccc |
| `passkey.spec.ts` | passkey@ownscale.test | dddd |
| frontend workers 0-2 | worker0-2@ownscale.test (invite invitee0-2) | eeee, ffff, gggg |
| ssh suite | ssh@ownscale.test | hhhh |
| serve suite | serve@ownscale.test | iiii |

The setup project signs them in, in that order (new organizations take the
next fixed subdomain, `aaaa`-`jjjj`), and records their organizations in
`tests/.auth/accounts.json`. Specs never hard-code an organization: they get
it from the `account` fixture, so `make e2e-remote` works with whatever names
the remote stack assigned.

`make e2e` installs and runs the setup once, then runs the frontend suite
(3 workers) and the connectivity suite concurrently, and the ssh suite
afterwards on its own: devbox containers add and remove host network links,
which makes Chromium abort other browsers' in-flight requests with
`net::ERR_NETWORK_CHANGED`. Each suite can still be run on its own.

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
- On the local stack `ignoreHTTPSErrors` is on (`localTLS` in
  `tests/connectivity/environment.ts`) instead of requiring every contributor
  to trust the local dev CA (see `make load-cert`). With `E2E_CA_FILE=`
  (the `e2e-remote*` targets) certificates are validated against the system
  store. Node clients such as the funnel request trust the CA file directly.
- A `setup` project (`tests/global.setup.ts`) logs in once as the Dex
  seeded `admin@ownscale.test` user and caches the session in
  `tests/.auth/admin.json` (gitignored); every other spec starts already
  authenticated. Specs that need to exercise the login/logout flow itself
  (`login.spec.ts`, `onboarding.spec.ts`) opt out of that cached state.
  That same setup also self-registers a fixed `passkey@ownscale.test`
  identity via passkey, creating org `dddd`, and caches its WebAuthn
  credential in `tests/.auth/passkey-credential.<base-domain>.json`
  (gitignored) so `passkey.spec.ts` and later runs can sign back in as that
  identity without registering another brand-new user/org. The file is per
  base domain, so local and `e2e-remote` runs don't invalidate each other's
  credential.
- `settings-apply.spec.ts` checks that saved settings take effect inside the
  organization's headscale (which restarts on every save). DNS changes are
  read back from a real enrolled device's netmap (via `tscl-bridge`'s `dns`
  op), and the login allowlist is checked by starting a registration login at
  `https://aaaa.<base-domain>/register/<random auth id>`: headscale answers
  401 when the signed-in user is excluded and 410 when admitted. Like
  `registerRealMachine`, it needs `aaaa.<base-domain>` to resolve.
- This suite is local-only for now (like `make test-integration`) and
  isn't wired into GitHub Actions CI.

## Tailscale SSH

`make e2e-ssh` (`npm run test:ssh` from `e2e/frontend/`) checks that
Tailscale SSH works between two real machines only while the org's policy
has an `ssh` rule. It starts two containers from the `e2e/devbox` image
joins both to `aaaa` with `osa devbox-key`, runs `tailscale up --ssh` on one
and `tailscale ssh` from the other, then removes the rule and expects SSH to be
denied. The containers are removed and the original policy restored afterwards.

Besides `make dev-up`, it needs docker with `/dev/net/tun` (the containers
run `tailscaled` with `NET_ADMIN`), the local dev CA at
`data/local-ca/root.crt` (or `E2E_CA_FILE`), and the `aaaa.ownscale.test`
`/etc/hosts` entry. It has its own config (`playwright.ssh.config.ts`) so
`make e2e-frontend` doesn't need docker.

The same config runs `tests/devbox/split-dns.spec.ts`: one devbox runs dnsmasq
on its tailnet IP, the DNS page points a split DNS domain at it, and a second
devbox must resolve names under that domain through MagicDNS only while
the entry exists.

These suites are local-only: devboxes join the local stack's docker network,
reach the org via `host-gateway`, trust the local dev CA, and get their keys
from `docker compose exec ownscale osa devbox-key`. `make e2e-remote` doesn't
run them.

## HTTPS certificates (extra tests)

`make e2e-remote-serve` (`npm run test:serve`, own config
`playwright.serve.config.ts`, part of `make e2e-remote`) checks that a machine
gets a publicly trusted certificate for its MagicDNS name, like
`tailscale serve` does: one `tscl-bridge` machine serves HTTPS on port 443 and a
second one fetches it, verifying the certificate against the system roots. It
runs as `serve@ownscale.test` (a remote stack's Dex gets it from `etc/dex` on
deploy).

Each run issues a Let's Encrypt certificate for a new hostname, which counts
towards the org's `CERTS_PER_ORG_PER_WEEK` and Let's Encrypt's own limits, so
it's skipped unless opted in per base domain with `EXTRA_TESTS`, a
comma-separated list of `<base domain>:<test>` entries:

```sh
EXTRA_TESTS=beastbb.bond:serve make e2e-remote-serve
```

Stacks without a DNS provider (like the local one) can't issue certificates.

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

Each live suite acquires its own lock exclusively (`e2e/.playwright-run.lock`,
`e2e/.playwright.ssh-run.lock`, `e2e/.playwright.connectivity-run.lock`),
outside Playwright's output directories. `--list` does not acquire it.
An existing lock is never automatically discarded, even if its PID appears
dead. Different suites may run at once (they use disjoint organizations), but
don't run the same suite twice concurrently or change its organizations
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

## Stress test

`e2e/stress` joins many ephemeral tsnet clients to one organization with a
reusable pre-auth key and keeps them connected, loading its headscale's
control plane. Every 5s it prints counts of connected, failed and pending
clients plus join latency p50/p95/max. On exit (after `-duration`, or on
Ctrl-C with `-duration 0`) it logs every client out so headscale deletes
the nodes right away.

```sh
make stress ORG=aaaa KEY=hskey-auth-... STRESS_ARGS="-n 100 -ramp 50ms -duration 10m"
# or directly:
SSL_CERT_FILE=$PWD/data/local-ca/root.crt STRESS_AUTHKEY=hskey-auth-... \
  go run ./e2e/stress -subdomain aaaa -n 100
```
