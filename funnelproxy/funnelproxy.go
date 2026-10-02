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
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/hscl"
)

const FunnelHeadscaleUserName = "ownscale-funnel"

// Queries is the narrow subset of *db.TxQueries the proxy needs.
type Queries interface {
	GetFunnelRouteBySubdomainAndLabel(ctx context.Context, arg sqlcgen.GetFunnelRouteBySubdomainAndLabelParams) (sqlcgen.FunnelRoute, error)
}

// HeadscaleClients returns an organization's headscale client. Implemented
// by *orgs.Service.
type HeadscaleClients interface {
	Client(subdomain string) (*hscl.Client, error)
}

// Pool proxies incoming funnel HTTP requests into the target
// organization's tailnet. It lazily starts, and caches for the process's
// lifetime, one tsnet.Server per organization subdomain that has ever
// received a funnel request.
type Pool struct {
	queries Queries
	hs      HeadscaleClients
	dataDir string
	logger  *slog.Logger

	mu      sync.Mutex
	servers map[string]*tsnet.Server // keyed by organization subdomain

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

// PortLookup returns the TCP port for subdomain's headscale instance, or
// false if no instance is running. Implemented by *orgs.Service.
type PortLookup interface {
	LookupPort(subdomain string) (int, bool)
}

// NewPool builds a Pool. dataDir is used to give each org's tsnet.Server
// a stable on-disk state directory (funnel-tsnet/<subdomain>) so it
// registers as a stable tailnet node across process restarts instead of
// re-registering on every request. portLookup maps subdomains to the TCP
// port where their in-process headscale instance is listening.
func NewPool(queries Queries, hs HeadscaleClients, dataDir string, logger *slog.Logger, portLookup PortLookup) *Pool {
	// The symptoms of having this enabled: only funnels from one org work,
	// all others timeout.
	netns.SetEnabled(false)

	return &Pool{
		queries:   queries,
		hs:        hs,
		dataDir:   dataDir,
		logger:    logger,
		servers:   make(map[string]*tsnet.Server),
		upTimeout: funnelUpTimeout,
		controlURL: func(subdomain string) (string, error) {
			port, ok := portLookup.LookupPort(subdomain)
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
	for subdomain, srv := range p.servers {
		if err := srv.Close(); err != nil {
			p.logger.Warn("close funnel tsnet server", "subdomain", subdomain, "error", err)
		}
	}
	p.servers = make(map[string]*tsnet.Server)
}

// Evict drops subdomain's cached tsnet.Server, so the next request builds a
// new one. Needed after the org's headscale restarts: the cached client's
// control connection stays attached to the stopped instance.
func (p *Pool) Evict(subdomain string) {
	p.mu.Lock()
	srv := p.servers[subdomain]
	delete(p.servers, subdomain)
	p.mu.Unlock()
	if srv != nil {
		go func() { _ = srv.Close() }() // can be slow; don't block the caller
	}
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
	if !ok || sub == "" {
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

	client, err := p.hs.Client(subdomain)
	if err != nil {
		p.logger.Error("funnel: resolve headscale client", "subdomain", subdomain, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	targetIP, err := targetNodeIP(ctx, client, route.TargetNodeID)
	if err != nil {
		p.logger.Warn("funnel: target node unreachable", "subdomain", subdomain, "target_node_id", route.TargetNodeID, "error", err)
		http.Error(w, "target machine not found", http.StatusBadGateway)
		return
	}

	srv, err := p.getOrCreateServer(ctx, subdomain, client)
	if err != nil {
		p.logger.Error("funnel: start tsnet server", "subdomain", subdomain, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
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
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				ctx, cancel := context.WithTimeout(ctx, funnelDialTimeout)
				defer cancel()
				return srv.Dial(ctx, network, addr)
			},
		},
		// Same as the default ErrorHandler, except it uses our logger.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
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

func targetNodeIP(ctx context.Context, client *hscl.Client, nodeID string) (string, error) {
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.ID != nodeID {
			continue
		}
		for _, addr := range n.IPAddresses {
			if !strings.Contains(addr, ":") { // prefer the IPv4 address
				return addr, nil
			}
		}
		if len(n.IPAddresses) > 0 {
			return n.IPAddresses[0], nil
		}
		return "", fmt.Errorf("node %q has no IP addresses", nodeID)
	}
	return "", fmt.Errorf("node %q not found", nodeID)
}

// funnelUpTimeout is how long a tsnet.Server may take to register and
// reach the Running state.
const funnelUpTimeout = 45 * time.Second

// EnsureServer starts or reuses the already-cached tsnet.Server for subdomain.
func (p *Pool) EnsureServer(ctx context.Context, subdomain string, client *hscl.Client) error {
	_, err := p.getOrCreateServer(ctx, subdomain, client)
	return err
}

// getOrCreateServer returns the cached tsnet.Server for subdomain,
// starting (and registering, via a freshly minted pre-auth key) one on
// first use.
func (p *Pool) getOrCreateServer(ctx context.Context, subdomain string, client *hscl.Client) (_ *tsnet.Server, err error) {
	defer derrors.Wrap(&err, "p.getOrCreateServer(%q)", subdomain)

	p.mu.Lock()
	if srv, ok := p.servers[subdomain]; ok {
		p.mu.Unlock()
		return srv, nil
	}
	p.mu.Unlock()

	// singleflight.Do serializes concurrent callers for the same
	// subdomain: only one of them mints a preauth key and starts a
	// tsnet.Server, and the rest wait for and share its result.
	// Without this, concurrent tsnet.Servers would work with and corrupt
	// the same state on disk.
	v, err, _ := p.sf.Do(subdomain, func() (any, error) {
		p.mu.Lock()
		if srv, ok := p.servers[subdomain]; ok {
			p.mu.Unlock()
			return srv, nil
		}
		p.mu.Unlock()

		authKey, err := ensureFunnelAuthKey(ctx, client)
		if err != nil {
			return nil, fmt.Errorf("mint pre-auth key: %w", err)
		}

		controlURL, err := p.controlURL(subdomain)
		if err != nil {
			return nil, fmt.Errorf("resolve control URL: %w", err)
		}

		srv := &tsnet.Server{
			Dir:        filepath.Join(p.dataDir, "funnel-tsnet", subdomain),
			Hostname:   "funnel-" + subdomain,
			AuthKey:    authKey.Key,
			ControlURL: controlURL,
			Logf:       tsnetLogf(p.logger, subdomain),
			UserLogf:   tsnetLogf(p.logger, subdomain),
		}

		upCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.upTimeout)
		defer cancel()
		if _, err := srv.Up(upCtx); err != nil {
			_ = srv.Close()
			if errors.Is(upCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("tsnet server up: timed out after %s: %w", p.upTimeout, err)
			}
			return nil, fmt.Errorf("tsnet server up: %w", err)
		}

		// Wait for DERP to be established before declaring the server
		// ready. srv.Up only waits for the control-plane connection, not
		// for DERP relay probing to complete; a custom DERP region that
		// is temporarily down can delay convergence by its probe timeout.
		if err := waitForDERP(context.Background(), srv, p.upTimeout); err != nil {
			p.logger.Warn("funnel: DERP not ready after wait; first dials may fail", "subdomain", subdomain, "error", err)
		}

		if err := client.DeletePreAuthKey(context.WithoutCancel(ctx), authKey.ID); err != nil {
			p.logger.Warn("funnel: delete pre-auth key after use", "subdomain", subdomain, "error", err)
		}

		p.mu.Lock()
		p.servers[subdomain] = srv
		p.mu.Unlock()
		return srv, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*tsnet.Server), nil
}

const funnelAuthKeyValidity = 100 * 365 * 24 * time.Hour

func ensureFunnelAuthKey(ctx context.Context, client *hscl.Client) (hscl.PreAuthKey, error) {
	// TODO: This listing might be inefficient.
	users, err := client.ListUsers(ctx)
	if err != nil {
		return hscl.PreAuthKey{}, err
	}
	var userID string
	for _, u := range users {
		if u.Name == FunnelHeadscaleUserName {
			userID = u.ID
			break
		}
	}
	if userID == "" {
		created, err := client.CreateUser(ctx, hscl.CreateUserParams{Name: FunnelHeadscaleUserName})
		if err != nil {
			return hscl.PreAuthKey{}, err
		}
		userID = created.ID
	}

	return client.CreatePreAuthKey(ctx, hscl.CreatePreAuthKeyParams{
		UserID:     userID,
		Reusable:   true,
		Ephemeral:  false,
		Expiration: time.Now().Add(funnelAuthKeyValidity),
	})
}
