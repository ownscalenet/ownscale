package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// SubdomainProxy wraps an http.Handler. Requests whose Host header matches
// a non-console subdomain of baseDomain are reverse-proxied to the port
// returned by lookupPort. All other requests fall through to next (the
// console mux: SPA, /api/*, OIDC provider).
type SubdomainProxy struct {
	next       http.Handler
	baseDomain string
	lookupPort func(subdomain string) (int, bool)
}

// NewSubdomainProxy builds a SubdomainProxy. lookupPort maps subdomains
// to the TCP port of their in-process headscale instance; a missing entry
// returns 503.
func NewSubdomainProxy(next http.Handler, baseDomain string, lookupPort func(subdomain string) (int, bool)) http.Handler {
	if baseDomain == "" {
		// No base domain configured (e.g. tests): pass everything through.
		return next
	}
	return &SubdomainProxy{
		next:       next,
		baseDomain: baseDomain,
		lookupPort: lookupPort,
	}
}

func (s *SubdomainProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subdomain, ok := s.extractSubdomain(r.Host)
	if !ok {
		s.next.ServeHTTP(w, r)
		return
	}

	port, ok := s.lookupPort(subdomain)
	if !ok {
		http.Error(w, "organization not available", http.StatusServiceUnavailable)
		return
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ServeHTTP(w, r)
}

// extractSubdomain returns the subdomain portion of host if host is of
// the form "<subdomain>.<baseDomain>" (with an optional port suffix) and
// the subdomain is not "console". Returns ("", false) otherwise.
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
	if sub == "console" {
		return "", false
	}
	return sub, true
}
