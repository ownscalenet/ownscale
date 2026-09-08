# tscl

An importable Go library representing one ephemeral Tailscale machine using
the official `tailscale.com/tsnet` client. It needs no `tailscaled` process,
TUN device, root privileges, or Docker container for the client itself.

This is a connectivity primitive, not an e2e runner. It does not log in through
the frontend, mint keys, provision organizations, or manage ACLs or funnels.

## Prerequisites

- A reachable headscale control URL, such as `https://aaaa.ownscale.test`.
  The Go process must resolve that hostname and reach the configured peers
  and/or DERP relay.
- A pre-auth key for each machine, supplied by the caller. Use distinct keys
  unless the key explicitly allows reuse. To test user-based ACLs, create
  keys owned by the intended users rather than tag-only keys.
- Trust the control server's CA in the Go process. Browser
  `ignoreHTTPSErrors` and `make load-cert` do not configure Go's trust.
  On Linux, set `SSL_CERT_FILE` to the absolute path of
  `data/local-ca/root.crt` before starting the process, or install the CA
  into the system trust store. TLS verification is never disabled.
- For a self-hosted test process, set `TS_NO_LOGS_NO_SUPPORT=true` before
  startup to disable Tailscale's upstream log uploads. An injected slog
  logger controls local diagnostics, not upstream logging.

No new module or Tailscale version is required; this package uses the root
repository's existing dependency.

## Two machines

From Go code, supply the keys without embedding them in source:

```go
func connectivity(ctx context.Context, controlURL, keyA, keyB string) (err error) {
    defer derrors.Wrap(&err, "connectivity()")

    a, err := tscl.New(ctx, tscl.Config{
        Hostname: "e2e-a", ControlURL: controlURL, AuthKey: keyA,
    })
    if err != nil {
        return err
    }
    defer func() { err = errors.Join(err, a.Close()) }()

    b, err := tscl.New(ctx, tscl.Config{
        Hostname: "e2e-b", ControlURL: controlURL, AuthKey: keyB,
    })
    if err != nil {
        return err
    }
    defer func() { err = errors.Join(err, b.Close()) }()

    endpoint, err := b.ServeHTTP(0)
    if err != nil {
        return err
    }
    target := "http://" + endpoint.Addresses()[0].String() + "/"
    result, err := a.ProbeHTTP(ctx, target, endpoint.Identity())
    if err != nil {
        return err
    }
    fmt.Println(result.Identity, result.StatusCode, result.Duration)
    return nil
}
```

Imports: `context`, `errors`, `fmt`,
`github.com/ownscalenet/ownscale/derrors`, and
`github.com/ownscalenet/ownscale/e2e/connectivity/tscl`.
Serve on A and probe from B as well when testing both directions.

## API behavior

- `New` requires a hostname, an absolute HTTP(S) control URL, and a nonblank
  pre-auth key. Startup defaults to 30 seconds; `StartupTimeout` overrides it.
  Cancellation only governs enrollment, not the returned client's lifetime.
- Each client has a fresh owned temporary directory, ephemeral registration,
  and a unique HTTP identity, even when hostnames match. Keys never appear in
  this wrapper's enrollment error messages or local tsnet logs.
- `Status(ctx)` returns fresh official tsnet status, including addresses and
  self information. Correlate these with ownscale's machine list; a tsnet
  identifier is not necessarily the headscale API machine ID.
- `DialContext` supports `tcp`, `tcp4`, and `tcp6` to **tailnet IP:port**
  destinations only. DNS names, public IPs, and subnet destinations are
  intentionally rejected: tsnet can otherwise route these through the host.
  For IPv6 use bracketed addresses. The caller owns the connection and must
  set deadlines for I/O after dialing.
- `Listen` wraps tsnet's tailnet-only TCP listener. The caller owns it.
  `ServeHTTP(port)` manages a listener and serves `GET /` with
  `{"identity":"..."}`. Port zero allocates a port; `Addresses()` reports the
  assigned port with IPv4/IPv6 addresses. All services on a client share its
  identity. `Wait(ctx)` exposes unexpected serve failures.
- `ProbeHTTP` expects an HTTP(S) URL with a tailnet IP and the destination's
  identity. It uses a fresh connection, no environment proxy, no redirects,
  a 4 KiB response limit, and a five-second default timeout (`ProbeTimeout`
  overrides it). Caller deadlines can shorten it. It reports non-200 status,
  invalid JSON, identity mismatch, and transport failure as errors, with
  partial observations retained in `ProbeResult`. Cancellation remains
  recognizable with `errors.Is`.
- `Close` is required, safe to repeat or call concurrently, and cancels
  outstanding operations. It closes managed HTTP services before the node
  and removes only its own temporary state. HTTP shutdown allows five seconds
  before forcing connections closed. Underlying `tsnet.Close` has no deadline
  API, so total shutdown is not guaranteed to complete within five seconds.
  Headscale's removal of ephemeral machine records is asynchronous.

Omitted loggers discard local diagnostics; injected loggers receive tsnet
diagnostics at debug level. Do not log keys or entire configurations in callers.

## Future e2e use

Playwright can orchestrate browser login, key creation, machine discovery, ACL
changes, and funnel creation. A later Go subprocess adapter can expose this
library to Node fixtures without putting browser concepts into `tscl`.

After ACL changes, poll with fresh bounded probes and restore the policy in
test cleanup. Serialize policy-changing tests or give them separate orgs.
A timeout alone is not proof of ACL enforcement: establish endpoint health
and positive controls before interpreting negative probes.

Different orgs reuse address ranges, so always verify the expected identity.
A cross-org IP may resolve to an unrelated machine or even the source itself.

For an ownscale funnel, target a `ServeHTTP` endpoint's machine and port, then
access its public URL from a normal browser/HTTP client outside tsnet and check
the identity. Ownscale funnels use its reverse proxy, not tsnet's native
`ListenFunnel`. No funnel management API is included here.

## Local package tests

```sh
go test -race ./e2e/connectivity/tscl
```

These tests use local HTTP/listener edges, including real tsnet startup against
an unavailable local control endpoint. They require neither a live ownscale
stack nor real credentials. They do not establish node-to-node connectivity;
run the two-machine example with explicitly supplied test keys for that.
