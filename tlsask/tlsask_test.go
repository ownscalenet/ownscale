package tlsask

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

func TestHandler(t *testing.T) {
	_, queries := dbtest.New(t)
	org := dbtest.CreateOrg(t, queries.Queries, "acme")
	if _, err := queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             "route1",
		OrganizationID: org.ID,
		Label:          "web",
		TargetNodeID:   "1",
		TargetPort:     80,
	}); err != nil {
		t.Fatalf("CreateFunnelRoute: %v", err)
	}

	tests := []struct {
		name          string
		contentDomain string
		domain        string
		want          int
	}{
		{"base domain", "funnel.net", "vpn.example.com", http.StatusOK},
		{"content domain", "funnel.net", "funnel.net", http.StatusOK},
		{"console", "funnel.net", "console.vpn.example.com", http.StatusOK},
		{"console uppercase trailing dot", "funnel.net", "CONSOLE.vpn.example.com.", http.StatusOK},
		{"existing org", "funnel.net", "acme.vpn.example.com", http.StatusOK},
		{"unknown org", "funnel.net", "other.vpn.example.com", http.StatusNotFound},
		{"nested subdomain", "funnel.net", "a.acme.vpn.example.com", http.StatusNotFound},
		{"existing funnel", "funnel.net", "web-acme.funnel.net", http.StatusOK},
		{"unknown funnel label", "funnel.net", "api-acme.funnel.net", http.StatusNotFound},
		{"funnel of unknown org", "funnel.net", "web-other.funnel.net", http.StatusNotFound},
		{"funnel on base domain", "vpn.example.com", "web-acme.vpn.example.com", http.StatusOK},
		{"org when funnel domain is base", "vpn.example.com", "acme.vpn.example.com", http.StatusOK},
		{"node name", "funnel.net", "web.netacme.os.funnel.net", http.StatusNotFound},
		{"node name on base domain", "vpn.example.com", "web.netacme.os.vpn.example.com", http.StatusNotFound},
		{"foreign domain", "funnel.net", "example.org", http.StatusNotFound},
		{"suffix lookalike", "funnel.net", "evilvpn.example.com", http.StatusNotFound},
		{"empty", "funnel.net", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := Handler(queries.Queries, "vpn.example.com", tt.contentDomain, slog.New(slog.DiscardHandler))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tls-allowed?domain="+url.QueryEscape(tt.domain), nil))
			if rec.Code != tt.want {
				t.Errorf("GET /tls-allowed?domain=%q = %d, want %d", tt.domain, rec.Code, tt.want)
			}
		})
	}
}
