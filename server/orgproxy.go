package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/ownscalenet/ownscale/orgs"
)

// orgProxy reverse-proxies requests to an organization's headscale, reached
// through dial.
type orgProxy struct {
	baseDomain string
	dial       func(ctx context.Context, subdomain string) (net.Conn, error)
	transport  *http.Transport
	logger     *slog.Logger
}

type subdomainKey struct{}

// proxyIdleConnTimeout is how long an idle keep-alive connection to an
// organization's headscale is kept.
const proxyIdleConnTimeout = 90 * time.Second

// newOrgProxy builds an orgProxy. dial connects to a subdomain's in-process
// headscale instance; an organization that isn't running (orgs.ErrNotRunning)
// gets a 503.
func newOrgProxy(baseDomain string, dial func(ctx context.Context, subdomain string) (net.Conn, error), logger *slog.Logger) *orgProxy {
	p := &orgProxy{baseDomain: baseDomain, dial: dial, logger: logger}
	// The address is ignored: the org comes from the request context.
	p.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			subdomain, _ := ctx.Value(subdomainKey{}).(string)
			return p.dial(ctx, subdomain)
		},
		// Open connections keep an org awake, so don't hold idle ones.
		IdleConnTimeout: proxyIdleConnTimeout,
	}
	return p
}

// serve proxies r to subdomain's headscale.
func (s *orgProxy) serve(w http.ResponseWriter, r *http.Request, subdomain string) {
	if s.handleKeyCORS(w, r) {
		return
	}

	start := time.Now()
	rec := &proxyRecorder{ResponseWriter: w, status: http.StatusOK}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: subdomain})
	proxy.Transport = s.transport
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		s.logger.Warn("proxy to organization failed", "subdomain", subdomain, "method", r.Method, "path", r.URL.Path, "error", err)
		if errors.Is(err, orgs.ErrNotRunning) {
			http.Error(w, "organization not available", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}
	proxy.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), subdomainKey{}, subdomain)))

	// For an upgraded connection (devices' control connections), this logs
	// once the connection ends, so duration is how long it stayed open.
	s.logger.Debug("proxied organization request",
		"subdomain", subdomain,
		"method", r.Method,
		"path", r.URL.Path,
		"status", rec.status,
		"upgraded", rec.hijacked,
		"remote_addr", r.RemoteAddr,
		"duration", time.Since(start),
	)
}

// keyPath is headscale's machine key endpoint. The console's in-browser
// Tailscale client (see handlers.CreateSSHSession) fetches it from the
// console origin; every other control request it makes is a websocket,
// which isn't subject to CORS.
const keyPath = "/key"

// handleKeyCORS allows the console origin to fetch GET /key, answering a
// preflight itself. It reports whether the request is fully handled.
func (s *orgProxy) handleKeyCORS(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != keyPath {
		return false
	}
	console := "https://" + orgs.ConsoleDomain(s.baseDomain)
	w.Header().Add("Vary", "Origin")
	if r.Header.Get("Origin") != console {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", console)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// proxyRecorder records the status a proxied request got. ReverseProxy
// answers protocol upgrades on the hijacked connection instead of through
// WriteHeader, so Hijack records those.
type proxyRecorder struct {
	http.ResponseWriter
	status   int
	hijacked bool
}

func (r *proxyRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *proxyRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err == nil {
		r.hijacked = true
		r.status = http.StatusSwitchingProtocols
	}
	return conn, brw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *proxyRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
