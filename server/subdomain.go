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
	"strings"
	"time"

	"github.com/ownscalenet/ownscale/orgs"
)

// SubdomainProxy wraps an http.Handler. Requests whose Host header matches
// a non-console subdomain of baseDomain are reverse-proxied to that
// organization's headscale, reached through dial. All other requests fall
// through to next (the console mux: SPA, /api/*, OIDC provider).
type SubdomainProxy struct {
	next       http.Handler
	baseDomain string
	dial       func(ctx context.Context, subdomain string) (net.Conn, error)
	transport  *http.Transport
	logger     *slog.Logger
}

type subdomainKey struct{}

// NewSubdomainProxy builds a SubdomainProxy. dial connects to a subdomain's
// in-process headscale instance; an organization that isn't running
// (orgs.ErrNotRunning) gets a 503.
func NewSubdomainProxy(next http.Handler, baseDomain string, dial func(ctx context.Context, subdomain string) (net.Conn, error), logger *slog.Logger) http.Handler {
	if baseDomain == "" {
		// No base domain configured (e.g. tests): pass everything through.
		return next
	}
	p := &SubdomainProxy{next: next, baseDomain: baseDomain, dial: dial, logger: logger}
	// The address is ignored: the org comes from the request context.
	p.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			subdomain, _ := ctx.Value(subdomainKey{}).(string)
			return p.dial(ctx, subdomain)
		},
	}
	return p
}

func (s *SubdomainProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subdomain, ok := s.extractSubdomain(r.Host)
	if !ok {
		s.next.ServeHTTP(w, r)
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

// extractSubdomain returns the subdomain portion of host if host is of
// the form "<subdomain>.<baseDomain>" (with an optional port suffix) and
// the subdomain is not orgs.ConsoleSubdomain. Returns ("", false) otherwise.
func (s *SubdomainProxy) extractSubdomain(host string) (string, bool) {
	// Strip port if present.
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	sub := strings.TrimSuffix(host, suffix)
	if sub == "" || strings.Contains(sub, ".") || strings.Contains(sub, "-") {
		return "", false
	}
	if sub == orgs.ConsoleSubdomain {
		return "", false
	}
	return sub, true
}
