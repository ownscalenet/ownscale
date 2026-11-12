// Package tlsask answers Caddy's on-demand TLS "ask" requests, so an
// operator's own Caddy can issue certificates per hostname, without a DNS
// plugin for wildcard certificates.
package tlsask

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/orgs"
)

// Queries is the subset of sqlcgen.Queries needed to check hostnames.
type Queries interface {
	GetOrganizationBySubdomain(ctx context.Context, subdomain string) (sqlcgen.Organization, error)
	GetFunnelRouteBySubdomainAndLabel(ctx context.Context, arg sqlcgen.GetFunnelRouteBySubdomainAndLabelParams) (sqlcgen.FunnelRoute, error)
}

// Handler serves GET /tls-allowed?domain=<host>: 200 if ownscale serves
// host, 404 otherwise. It reveals which organizations and funnels exist, so
// it must only be reachable by the reverse proxy, never publicly.
func Handler(q Queries, baseDomain, contentDomain string, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tls-allowed", func(w http.ResponseWriter, r *http.Request) {
		ok, err := allowed(r.Context(), q, baseDomain, contentDomain, r.URL.Query().Get("domain"))
		if err != nil {
			logger.Error("tls ask", "domain", r.URL.Query().Get("domain"), "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// allowed reports whether host is the base or content domain, the console, an
// existing organization or an existing funnel.
func allowed(ctx context.Context, q Queries, baseDomain, contentDomain, host string) (bool, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false, nil
	}
	if host == baseDomain || host == contentDomain || host == orgs.ConsoleSubdomain+"."+baseDomain {
		return true, nil
	}
	if label, sub, ok := funnelproxy.ParseHost(host, contentDomain); ok {
		_, err := q.GetFunnelRouteBySubdomainAndLabel(ctx, sqlcgen.GetFunnelRouteBySubdomainAndLabelParams{Subdomain: sub, Label: label})
		return found(err)
	}
	sub, ok := strings.CutSuffix(host, "."+baseDomain)
	if !ok || sub == "" || strings.Contains(sub, ".") {
		return false, nil
	}
	_, err := q.GetOrganizationBySubdomain(ctx, sub)
	return found(err)
}

func found(err error) (bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
