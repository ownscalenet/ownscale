// Package funnelproxy implements the "funnel" feature's public-internet
// side: an in-process reverse proxy that terminates funnel requests
// forwarded by Caddy (see etc/caddy/Caddyfile's "*.*.{$BASE_DOMAIN}" site
// block) and forwards them into the target organization's tailnet, using
// an embedded tailscale.com/tsnet client in place of a real device's own
// tailscaled.
package funnelproxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/hscl"
)

// AgentHeadscaleUserName is the headscale user owning each org's funnel node.
// It's reserved: members are never given this name.
const AgentHeadscaleUserName = "ownscale-agent"

func IsAgentUser(u *hscl.User) bool {
	return u != nil && u.Name == AgentHeadscaleUserName && u.ProviderID == ""
}

// Queries is the narrow subset of *db.TxQueries the proxy needs.
type Queries interface {
	GetFunnelRouteBySubdomainAndLabel(ctx context.Context, arg sqlcgen.GetFunnelRouteBySubdomainAndLabelParams) (sqlcgen.FunnelRoute, error)
}

// Orgs gives access to running organizations' headscale instances, without
// waking them. Implemented by *orgs.Service.
type Orgs interface {
	Client(subdomain string) (*hscl.Client, error)
	// LookupPort returns the TCP port of subdomain's headscale instance, or
	// false if none is running.
	LookupPort(subdomain string) (int, bool)
}

// Pool proxies incoming funnel HTTP requests into the target
// organization's tailnet. It lazily starts, and caches for the process's
// lifetime, one tsnet.Server per organization subdomain that has ever
// received a funnel request.
type Pool struct {
	queries Queries
	orgs    Orgs
	dataDir string
	logger  *slog.Logger

	cfg config.FunnelConfig
	// limitsURL is the docs section on funnel limits, linked from error pages.
	limitsURL string

	mu      sync.Mutex
	servers map[string]*tunnel    // keyed by organization subdomain
	limits  map[string]*orgLimits // keyed by organization subdomain

	// sf de-duplicates concurrent getOrCreateServer calls for the same
	// subdomain, so a burst of near-simultaneous requests for a
	// brand-new funnel never mints more than one preauth key or starts more
	// than one tsnet.Server against the same on-disk state directory.
	sf        singleflight.Group
	upTimeout time.Duration
	// controlURL builds the tsnet ControlURL for a subdomain: the org's
	// in-process headscale on loopback. Overridden in tests.
	controlURL func(subdomain string) (string, error)
}

// tunnel is an org's tsnet node and the transport proxying through it,
// shared by all requests so connections to funnel targets are reused.
type tunnel struct {
	srv       *tsnet.Server
	transport *http.Transport
	// status returns srv's netmap; replaced in tests.
	status func(context.Context) (*ipnstate.Status, error)
}

func newTunnel(srv *tsnet.Server) *tunnel {
	return &tunnel{
		srv: srv,
		status: func(ctx context.Context) (*ipnstate.Status, error) {
			lc, err := srv.LocalClient()
			if err != nil {
				return nil, err
			}
			return lc.Status(ctx)
		},
		transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				ctx, cancel := context.WithTimeout(ctx, funnelDialTimeout)
				defer cancel()
				return srv.Dial(ctx, network, addr)
			},
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
}

func (t *tunnel) close() error {
	t.transport.CloseIdleConnections()
	if t.srv == nil {
		return nil
	}
	return t.srv.Close()
}

// NewPool builds a Pool. Each org's tsnet.Server keeps its state under
// cfg.DataDir, so it stays the same tailnet node across restarts. cfg.Funnel
// sets the limits, and the error pages link to the docs on cfg.BaseDomain.
func NewPool(queries Queries, orgs Orgs, cfg *config.Config, logger *slog.Logger) *Pool {
	// The symptoms of having this enabled: only funnels from one org work,
	// all others timeout.
	netns.SetEnabled(false)

	return &Pool{
		queries:   queries,
		orgs:      orgs,
		dataDir:   cfg.DataDir,
		logger:    logger,
		cfg:       cfg.Funnel,
		limitsURL: "https://" + cfg.BaseDomain + "/docs/features/funnels/#limits",
		servers:   make(map[string]*tunnel),
		limits:    make(map[string]*orgLimits),
		upTimeout: funnelUpTimeout,
		controlURL: func(subdomain string) (string, error) {
			port, ok := orgs.LookupPort(subdomain)
			if !ok {
				return "", fmt.Errorf("no headscale instance running for %q", subdomain)
			}
			return fmt.Sprintf("http://127.0.0.1:%d", port), nil
		},
	}
}

// Close shuts down every cached tsnet.Server. Intended to be called once,
// at process shutdown.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for subdomain, tun := range p.servers {
		if err := tun.close(); err != nil {
			p.logger.Warn("close funnel tsnet server", "subdomain", subdomain, "error", err)
		}
	}
	p.servers = make(map[string]*tunnel)
}

// Evict drops subdomain's cached tsnet.Server, so the next request builds a
// new one. Needed after the org's headscale restarts: the cached client's
// control connection stays attached to the stopped instance.
func (p *Pool) Evict(subdomain string) {
	p.mu.Lock()
	tun := p.servers[subdomain]
	delete(p.servers, subdomain)
	p.mu.Unlock()
	if tun != nil {
		go func() { _ = tun.close() }() // can be slow; don't block the caller
	}
}

// Forget drops subdomain's cached tsnet.Server and deletes its on-disk
// state. Call it after the org's funnel node is deleted from headscale, so
// the next funnel registers a new node instead of resuming as the deleted
// one. Closing the server can take seconds.
func (p *Pool) Forget(subdomain string) (err error) {
	defer derrors.Wrap(&err, "p.Forget(%q)", subdomain)
	// Shares getOrCreateServer's singleflight key, so no server is started
	// on the directory while it's removed.
	_, err, _ = p.sf.Do(subdomain, func() (any, error) {
		p.mu.Lock()
		tun := p.servers[subdomain]
		delete(p.servers, subdomain)
		p.mu.Unlock()
		// Closed first: the server writes its state by path until then.
		if tun != nil {
			if err := tun.close(); err != nil {
				p.logger.Warn("funnel: close tsnet server", "subdomain", subdomain, "error", err)
			}
		}
		return nil, os.RemoveAll(p.stateDir(subdomain))
	})
	return err
}

// stateDir is subdomain's tsnet.Server state directory.
func (p *Pool) stateDir(subdomain string) string {
	return filepath.Join(p.dataDir, subdomain, "funnel-tsnet")
}

// tsnetLogf adapts a tsnet.Server's Logf to the app's structured logger.
func tsnetLogf(logger *slog.Logger, subdomain string) func(format string, args ...any) {
	return func(format string, args ...any) {
		logger.Debug("tsnet", "subdomain", subdomain, "msg", fmt.Sprintf(format, args...))
	}
}

// ParseHost splits a funnel request's Host header into (label, subdomain, ok),
// given base. ok is false if host isn't shaped like a funnel hostname at all.
func ParseHost(host, base string) (label, subdomain string, ok bool) {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	sub, ok := strings.CutSuffix(host, "."+base)
	// Deeper names, e.g. node names under Config.TailnetDomain, aren't funnels.
	if !ok || sub == "" || strings.Contains(sub, ".") {
		return "", "", false
	}
	label, subdomain, ok = strings.Cut(sub, "-")
	if !ok || label == "" || subdomain == "" {
		return "", "", false
	}
	return label, subdomain, true
}

// ServeHTTP handles a single funnel request: r.Host is expected to already
// have been matched by ParseHost by the caller (see server/routes.go).
func (p *Pool) ServeHTTP(w http.ResponseWriter, r *http.Request, label, subdomain string) {
	ctx := r.Context()

	route, err := p.queries.GetFunnelRouteBySubdomainAndLabel(ctx, sqlcgen.GetFunnelRouteBySubdomainAndLabelParams{
		Subdomain: subdomain,
		Label:     label,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		p.logger.Error("funnel: look up route", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	lim := p.limitsFor(subdomain)
	if !lim.rate.Allow() {
		w.Header().Set("Retry-After", "1")
		p.writeErrorPage(w, http.StatusTooManyRequests, msgTooManyRequests)
		return
	}
	select {
	case lim.inFlight <- struct{}{}:
		defer func() { <-lim.inFlight }()
	default:
		w.Header().Set("Retry-After", "1")
		p.writeErrorPage(w, http.StatusTooManyRequests, msgTooManyRequests)
		return
	}

	if r.ContentLength > p.cfg.MaxBodyBytes {
		p.writeErrorPage(w, http.StatusRequestEntityTooLarge, msgBodyTooLarge)
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, p.cfg.MaxBodyBytes)
	}

	// A sleeping org has no online machines, so its funnel target is offline
	// too. Waking it would only let anyone keep orgs awake.
	client, err := p.orgs.Client(subdomain)
	if err != nil {
		http.Error(w, "funnel target offline", http.StatusServiceUnavailable)
		return
	}

	tun, err := p.getOrCreateServer(ctx, subdomain, client)
	if err != nil {
		p.logger.Error("funnel: start tsnet server", "subdomain", subdomain, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	targetIP, err := tun.peerIP(ctx, route.TargetNodeID)
	if err != nil {
		p.logger.Warn("funnel: target node unreachable", "subdomain", subdomain, "target_node_id", route.TargetNodeID, "error", err)
		http.Error(w, "target machine not found", http.StatusBadGateway)
		return
	}

	targetAddr := net.JoinHostPort(targetIP, strconv.FormatInt(route.TargetPort, 10))
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = targetAddr
			// Preserve the public-facing Host header rather than
			// overwriting it with targetAddr -- matches how Caddy's own
			// reverse_proxy already behaves, and how a
			// target app listening on a fixed port has no reason to
			// expect anything else.
			pr.Out.Host = pr.In.Host
		},
		Transport: tun.transport,
		// Same as the default ErrorHandler, except it uses our logger.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				p.writeErrorPage(w, http.StatusRequestEntityTooLarge, msgBodyTooLarge)
				return
			}
			p.logger.Warn("funnel: proxy to target failed", "subdomain", subdomain, "label", label, "target", targetAddr, "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// funnelDialTimeout limits connecting to a funnel's target machine.
// Without it the Dial hangs forever.
const funnelDialTimeout = 10 * time.Second

// waitForDERP polls lc.Status until the tsnet node has a relay (DERP
// region) assigned, which means it can route packets to peers. It returns
// when relay is set, when ctx is done, or after timeout, whichever is first.
func waitForDERP(ctx context.Context, srv *tsnet.Server, timeout time.Duration) error {
	lc, err := srv.LocalClient()
	if err != nil {
		return fmt.Errorf("get local client: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		st, err := lc.Status(ctx)
		if err == nil && st.Self != nil && st.Self.Relay != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// peerIP returns the Tailscale IP of the peer with headscale node ID
// nodeID, from the tsnet node's netmap, so requests don't query headscale.
func (t *tunnel) peerIP(ctx context.Context, nodeID string) (string, error) {
	st, err := t.status(ctx)
	if err != nil {
		return "", err
	}
	return peerIP(st, nodeID)
}

// peerIP prefers IPv4. headscale's StableNodeID is the decimal node ID.
func peerIP(st *ipnstate.Status, nodeID string) (string, error) {
	for _, ps := range st.Peer {
		if ps.ID != tailcfg.StableNodeID(nodeID) {
			continue
		}
		for _, ip := range ps.TailscaleIPs {
			if ip.Is4() {
				return ip.String(), nil
			}
		}
		if len(ps.TailscaleIPs) > 0 {
			return ps.TailscaleIPs[0].String(), nil
		}
		return "", fmt.Errorf("node %q has no IP addresses", nodeID)
	}
	return "", fmt.Errorf("node %q not in netmap", nodeID)
}

// funnelUpTimeout is how long a tsnet.Server may take to register and
// reach the Running state.
const funnelUpTimeout = 45 * time.Second

// EnsureServer starts or reuses the already-cached tsnet.Server for subdomain.
func (p *Pool) EnsureServer(ctx context.Context, subdomain string, client *hscl.Client) error {
	_, err := p.getOrCreateServer(ctx, subdomain, client)
	return err
}

// getOrCreateServer returns the cached tunnel for subdomain,
// starting (and registering, via a freshly minted pre-auth key) one on
// first use.
func (p *Pool) getOrCreateServer(ctx context.Context, subdomain string, client *hscl.Client) (_ *tunnel, err error) {
	defer derrors.Wrap(&err, "p.getOrCreateServer(%q)", subdomain)

	p.mu.Lock()
	if tun, ok := p.servers[subdomain]; ok {
		p.mu.Unlock()
		return tun, nil
	}
	p.mu.Unlock()

	// singleflight.Do serializes concurrent callers for the same
	// subdomain: only one of them mints a preauth key and starts a
	// tsnet.Server, and the rest wait for and share its result.
	// Without this, concurrent tsnet.Servers would work with and corrupt
	// the same state on disk.
	for {
		v, err, _ := p.sf.Do(subdomain, func() (any, error) {
			p.mu.Lock()
			if tun, ok := p.servers[subdomain]; ok {
				p.mu.Unlock()
				return tun, nil
			}
			p.mu.Unlock()

			authKey, err := ensureFunnelAuthKey(ctx, client)
			if err != nil {
				return nil, fmt.Errorf("mint pre-auth key: %w", err)
			}
			defer func() {
				if err := client.DeletePreAuthKey(context.WithoutCancel(ctx), authKey.ID); err != nil {
					p.logger.Warn("funnel: delete pre-auth key", "subdomain", subdomain, "error", err)
				}
			}()

			controlURL, err := p.controlURL(subdomain)
			if err != nil {
				return nil, fmt.Errorf("resolve control URL: %w", err)
			}

			srv := &tsnet.Server{
				Dir:        p.stateDir(subdomain),
				Hostname:   agentHostname,
				AuthKey:    authKey.Key,
				ControlURL: controlURL,
				Logf:       tsnetLogf(p.logger, subdomain),
				UserLogf:   tsnetLogf(p.logger, subdomain),
			}

			upCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.upTimeout)
			defer cancel()
			st, err := srv.Up(upCtx)
			if err != nil {
				_ = srv.Close()
				if errors.Is(upCtx.Err(), context.DeadlineExceeded) {
					return nil, fmt.Errorf("tsnet server up: timed out after %s: %w", p.upTimeout, err)
				}
				return nil, fmt.Errorf("tsnet server up: %w", err)
			}

			if err := removeStaleFunnelNodes(upCtx, client, subdomain, string(st.Self.ID)); err != nil {
				p.logger.Warn("funnel: remove stale funnel machines", "subdomain", subdomain, "error", err)
			}

			// Wait for DERP to be established before declaring the server
			// ready. srv.Up only waits for the control-plane connection, not
			// for DERP relay probing to complete; a custom DERP region that
			// is temporarily down can delay convergence by its probe timeout.
			if err := waitForDERP(context.Background(), srv, p.upTimeout); err != nil {
				p.logger.Warn("funnel: DERP not ready after wait; first dials may fail", "subdomain", subdomain, "error", err)
			}

			tun := newTunnel(srv)
			p.mu.Lock()
			p.servers[subdomain] = tun
			p.mu.Unlock()
			return tun, nil
		})
		if err != nil {
			return nil, err
		}
		// v isn't a tunnel when this call joined a concurrent Forget.
		if tun, ok := v.(*tunnel); ok {
			return tun, nil
		}
	}
}

// agentHostname is the hostname given to each org's funnel/agent tsnet
// node. It's a plain constant (no subdomain suffix) since each org has its
// own isolated headscale tailnet.
// Careful if renaming this. The state associated with the node is preserved on
// disk and persists after a rename.
const agentHostname = "ownscale-agent"

// removeStaleFunnelNodes deletes subdomain's funnel machines other than
// selfID, then names selfID after the funnel again. Those machines are left
// behind when a funnel server lost its state, e.g. after restoring an org
// from backup, and nothing can reconnect as them.
func removeStaleFunnelNodes(ctx context.Context, client *hscl.Client, subdomain, selfID string) (err error) {
	defer derrors.Wrap(&err, "removeStaleFunnelNodes(%q, %q)", subdomain, selfID)

	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return err
	}
	name := agentHostname
	var errs []error
	var self *hscl.Node
	nameTaken := false
	for _, n := range nodes {
		switch {
		case n.ID == selfID:
			self = &n
		case IsAgentUser(n.User):
			errs = append(errs, client.DeleteNode(ctx, n.ID))
		case n.GivenName == name:
			nameTaken = true // by a member's machine
		}
	}
	// Renamed last: a deleted machine may have held the name.
	if self != nil && self.GivenName != name && !nameTaken {
		errs = append(errs, client.RenameNode(ctx, selfID, name))
	}
	return errors.Join(errs...)
}

// funnelAuthKeyValidity only needs to outlast srv.Up; the key is deleted right after.
const funnelAuthKeyValidity = 5 * time.Minute

func ensureFunnelAuthKey(ctx context.Context, client *hscl.Client) (hscl.PreAuthKey, error) {
	// TODO: This listing might be inefficient.
	users, err := client.ListUsers(ctx)
	if err != nil {
		return hscl.PreAuthKey{}, err
	}
	var userID string
	for _, u := range users {
		if IsAgentUser(&u) {
			userID = u.ID
			break
		}
	}
	if userID == "" {
		created, err := client.CreateUser(ctx, hscl.CreateUserParams{Name: AgentHeadscaleUserName})
		if err != nil {
			return hscl.PreAuthKey{}, err
		}
		userID = created.ID
	}

	return client.CreatePreAuthKey(ctx, hscl.CreatePreAuthKeyParams{
		UserID:     userID,
		Expiration: time.Now().Add(funnelAuthKeyValidity),
	})
}

// orgLimits are an org's funnel limits, so one org's funnel can't exhaust
// ownscale.
type orgLimits struct {
	rate     *rate.Limiter
	inFlight chan struct{}
}

// limitsFor returns subdomain's limits. Only called for existing routes, so
// the map is bounded by the number of orgs.
func (p *Pool) limitsFor(subdomain string) *orgLimits {
	p.mu.Lock()
	defer p.mu.Unlock()
	lim, ok := p.limits[subdomain]
	if !ok {
		lim = &orgLimits{
			rate:     rate.NewLimiter(rate.Limit(p.cfg.RatePerSecond), p.cfg.RateBurst),
			inFlight: make(chan struct{}, p.cfg.MaxConcurrent),
		}
		p.limits[subdomain] = lim
	}
	return lim
}

// Messages shown to funnel visitors when a limit is hit.
const (
	msgTooManyRequests = "This site is receiving too many requests. Try again in a moment."
	msgBodyTooLarge    = "The request body is too large."
)

var errorPage = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Status}} {{.Title}}</title>
<style>
body { font-family: system-ui, sans-serif; margin: 0; min-height: 100vh; display: grid; place-items: center; background: #f5f5f5; color: #222; }
main { max-width: 32rem; padding: 2rem; text-align: center; }
.brand { display: inline-flex; align-items: center; gap: .5rem; margin-bottom: 1.5rem; font-weight: 600; font-size: 1.125rem; color: #111827; }
.brand svg { width: 1.75rem; height: 1.75rem; }
h1 { font-size: 1.5rem; margin: 0 0 .5rem; }
p { margin: 0 0 .75rem; color: #555; }
a { color: #2563eb; }
</style>
</head>
<body>
<main>
<div class="brand">
<svg viewBox="0 0 64 64" aria-hidden="true"><g fill="currentColor"><circle cx="12" cy="12" r="8"/><circle cx="32" cy="12" r="8"/><circle cx="52" cy="12" r="8"/><circle cx="12" cy="32" r="8"/><circle cx="52" cy="32" r="8"/><circle cx="12" cy="52" r="8"/><circle cx="32" cy="52" r="8"/><circle cx="52" cy="52" r="8"/></g><circle cx="32" cy="32" r="8" fill="currentColor" opacity="0.4"/></svg>
ownscale
</div>
<h1>{{.Status}} {{.Title}}</h1>
<p>{{.Message}}</p>
<p>If you own this funnel, see the <a href="{{.LimitsURL}}">funnel limits</a>.</p>
</main>
</body>
</html>
`))

// writeErrorPage responds with a small HTML page, since funnel visitors are
// usually people with a browser.
func (p *Pool) writeErrorPage(w http.ResponseWriter, status int, message string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	// Replaces the console's CSP, which would block the inline <style>.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = errorPage.Execute(w, struct {
		Status                    int
		Title, Message, LimitsURL string
	}{status, http.StatusText(status), message, p.limitsURL})
}
