package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

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
}

type subdomainKey struct{}

// NewSubdomainProxy builds a SubdomainProxy. dial connects to a subdomain's
// in-process headscale instance; an organization that isn't running
// (orgs.ErrNotRunning) gets a 503.
func NewSubdomainProxy(next http.Handler, baseDomain string, dial func(ctx context.Context, subdomain string) (net.Conn, error)) http.Handler {
	if baseDomain == "" {
		// No base domain configured (e.g. tests): pass everything through.
		return next
	}
	p := &SubdomainProxy{next: next, baseDomain: baseDomain, dial: dial}
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

	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: subdomain})
	proxy.Transport = s.transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		if errors.Is(err, orgs.ErrNotRunning) {
			http.Error(w, "organization not available", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), subdomainKey{}, subdomain)))
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
