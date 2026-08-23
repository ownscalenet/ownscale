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

	// The bare base domain and the "admin" subdomain are always statically
	// routed by Caddy (not on-demand), but they still fall within the
	// "*.{base}" wildcard subject, for TLS.
	if domain == base || domain == "admin."+base || domain == "auth."+base || domain == "docs."+base || domain == "dex."+base {
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
