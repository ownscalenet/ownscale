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
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

// funnelHeadscaleUserName is the fixed headscale user name the proxy
// registers its per-org tsnet nodes under. It's not tied to any ownscale user.
// TODO: should this be hidden in the UI?
const funnelHeadscaleUserName = "ownscale-funnel"

// Queries is the narrow subset of *db.TxQueries the proxy needs.
type Queries interface {
	GetFunnelRouteBySubdomainAndLabel(ctx context.Context, arg sqlcgen.GetFunnelRouteBySubdomainAndLabelParams) (sqlcgen.FunnelRoute, error)
}

// HeadscaleClients is the narrow subset of *headscaleclient.Registry the proxy needs.
type HeadscaleClients interface {
	Client(ctx context.Context, subdomain string) (*headscaleclient.Client, error)
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
	// controlURL builds the tsnet ControlURL for a subdomain; defaults
	// to headscale's real container address via NewPool
	controlURL func(ctx context.Context, subdomain string) (string, error)
}

// NewPool builds a Pool. dataDir is the same directory dockerctl.Ctl uses
// for its own per-organization host state (each tsnet.Server gets its own
// subdirectory under dataDir/funnel-tsnet/<subdomain>, so it registers as
// a stable node across process restarts instead of re-registering on
// every request).
func NewPool(queries Queries, hs HeadscaleClients, dataDir string, logger *slog.Logger) *Pool {
	// Every tsnet.Server we run uses a fake, no-op tun device (there's no
	// real OS route through Tailscale to loop back into), so netns's
	// default-route-interface binding -- meant to stop a *real* tailscaled
	// from routing its own control-plane traffic back through its own tun
	// -- only causes harm here: this container has one Docker network per
	// organization, so it forces every tsnet control-client dial through
	// whichever org happens to currently own the container's default
	// route, breaking connectivity to every other org's headscale.
	// The symptoms are: only funnels from one org work, all others timeout.
	netns.SetEnabled(false)

	return &Pool{
		queries:   queries,
		hs:        hs,
		dataDir:   dataDir,
		logger:    logger,
		servers:   make(map[string]*tsnet.Server),
		upTimeout: funnelUpTimeout,
		controlURL: func(ctx context.Context, subdomain string) (string, error) {
			return resolveControlURL(ctx, dockerctl.HSContainerName(subdomain))
		},
	}
}

// resolveControlURL resolves host (a Docker-network container name) to its
// current literal IP address and returns an "http://<ip>:8080" ControlURL
// built from that IP rather than the bare DNS name.
//
// ts2021.NewClient has a fallback: if the request to a controlURL fails
// and the controlURL is not a private/loopback IP address, then subsequent
// requests are done on HTTPS/443. The internal address of headscale, ie
// http://hs-aaaa:8080, is not a private/loopback IP, so tailscale client
// switches to https/443 and continuously errors.
func resolveControlURL(ctx context.Context, host string) (_ string, err error) {
	defer derrors.Wrap(&err, "resolveControlURL(%q)", host)

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	// Prefer an IPv4 address to match the Docker bridge network's address
	// family; fall back to IPv6 only if no IPv4 address was returned.
	var ipv6 string
	for _, addr := range addrs {
		if ipv4 := addr.IP.To4(); ipv4 != nil {
			return "http://" + net.JoinHostPort(ipv4.String(), "8080"), nil
		}
		if ipv6 == "" {
			ipv6 = addr.IP.String()
		}
	}
	if ipv6 == "" {
		return "", fmt.Errorf("no addresses found for %q", host)
	}
	return "http://" + net.JoinHostPort(ipv6, "8080"), nil
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

// tsnetLogf adapts a tsnet.Server's Logf to the app's structured logger.
func tsnetLogf(logger *slog.Logger, subdomain string) func(format string, args ...any) {
	return func(format string, args ...any) {
		logger.Debug("tsnet", "subdomain", subdomain, "msg", fmt.Sprintf(format, args...))
	}
}

// ParseHost splits a funnel request's Host header (already stripped of
// any port) into (label, subdomain, ok), given base -- the same
// "label-subdomain.{base}" single-DNS-label scheme handlers/funnel.go
// computes and validates. ok is false if host isn't shaped like a funnel
// hostname at all.
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

	client, err := p.hs.Client(ctx, subdomain)
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
			// reverse_proxy to hs-<sub> already behaves, and how a
			// target app listening on a fixed port has no reason to
			// expect anything else.
			pr.Out.Host = pr.In.Host
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return srv.Dial(ctx, network, addr)
			},
		},
	}
	proxy.ServeHTTP(w, r)
}

func targetNodeIP(ctx context.Context, client *headscaleclient.Client, nodeID string) (string, error) {
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

// getOrCreateServer returns the cached tsnet.Server for subdomain,
// starting (and registering, via a freshly minted pre-auth key) one on
// first use.
func (p *Pool) getOrCreateServer(ctx context.Context, subdomain string, client *headscaleclient.Client) (_ *tsnet.Server, err error) {
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

		controlURL, err := p.controlURL(ctx, subdomain)
		if err != nil {
			return nil, fmt.Errorf("resolve control URL: %w", err)
		}

		srv := &tsnet.Server{
			Dir:        filepath.Join(p.dataDir, "funnel-tsnet", subdomain),
			Hostname:   "funnel-" + subdomain,
			AuthKey:    authKey,
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

func ensureFunnelAuthKey(ctx context.Context, client *headscaleclient.Client) (string, error) {
	// TODO: This listing might be inefficient.
	users, err := client.ListUsers(ctx)
	if err != nil {
		return "", err
	}
	var userID string
	for _, u := range users {
		if u.Name == funnelHeadscaleUserName {
			userID = u.ID
			break
		}
	}
	if userID == "" {
		created, err := client.CreateUser(ctx, headscaleclient.CreateUserParams{Name: funnelHeadscaleUserName})
		if err != nil {
			return "", err
		}
		userID = created.ID
	}

	key, err := client.CreatePreAuthKey(ctx, headscaleclient.CreatePreAuthKeyParams{
		UserID:     userID,
		Reusable:   true,
		Ephemeral:  false,
		Expiration: time.Now().Add(funnelAuthKeyValidity),
	})
	if err != nil {
		return "", err
	}
	return key.Key, nil
}
