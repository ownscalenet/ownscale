package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// TLSAsk is Caddy's on-demand TLS "ask" endpoint. Caddy calls
// GET /api/tls-ask?domain=<host> before issuing a certificate.
func (h *Handlers) TLSAsk(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	base := h.baseDomain
	if domain == "" || base == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// docs.{base} and dex.{base} are fixed, known-in-advance subdomains
	// (unlike per-organization ones, which need the
	// GetOrganizationBySubdomain check below); they still use on-demand
	// TLS (see etc/caddy/Caddyfile), so they go through this endpoint, but
	// are always allowed rather than looked up. The bare base domain gets
	// eager/static TLS and doesn't call this endpoint at all, but is
	// included here too since it still falls within the "*.{base}"
	// wildcard subject.
	if domain == base || domain == "docs."+base || domain == "dex."+base {
		w.WriteHeader(http.StatusOK)
		return
	}
	sub, ok := strings.CutSuffix(domain, "."+base)
	if !ok || sub == "" || strings.Contains(sub, ".") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, err := h.queries.GetOrganizationBySubdomain(r.Context(), sub)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err != nil {
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
