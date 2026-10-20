package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeFunnelIfMatched(t *testing.T) {
	const contentDomain = "example-content.net"
	tests := []struct {
		name          string
		contentDomain string
		host          string
		wantHandled   bool
	}{
		{"funnel host", testBaseDomain, "web-acme." + testBaseDomain, true},
		{"plain org subdomain", testBaseDomain, "acme." + testBaseDomain, false},
		{"unrelated host", testBaseDomain, "evil.com", false},
		{"separate domain funnel host", contentDomain, "web-acme." + contentDomain, true},
		{"separate domain ignores base domain", contentDomain, "web-acme." + testBaseDomain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandlers(t)
			h.baseDomain = testBaseDomain
			h.contentDomain = tt.contentDomain
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			handled := h.ServeFunnelIfMatched(rec, req)
			if handled != tt.wantHandled {
				t.Errorf("ServeFunnelIfMatched(host=%q) = %v, want %v", tt.host, handled, tt.wantHandled)
			}
		})
	}
}

func TestServeFunnelIfMatchedNoContentDomainConfigured(t *testing.T) {
	h, _ := newTestHandlers(t)
	// contentDomain left empty, as in tests without a base domain.

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "web-acme.example.com"
	rec := httptest.NewRecorder()
	if h.ServeFunnelIfMatched(rec, req) {
		t.Error("ServeFunnelIfMatched = true with no contentDomain configured, want false")
	}
}
