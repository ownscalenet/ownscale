package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeFunnelIfMatched(t *testing.T) {
	h, _ := newTestHandlers(t, false)
	h.baseDomain = testBaseDomain

	tests := []struct {
		name        string
		host        string
		wantHandled bool
	}{
		{"funnel host", "web-acme." + testBaseDomain, true},
		{"plain org subdomain", "acme." + testBaseDomain, false},
		{"unrelated host", "evil.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestServeFunnelIfMatchedNoBaseDomainConfigured(t *testing.T) {
	h, _ := newTestHandlers(t, false)
	// baseDomain left empty, as when running without provisioning configured.

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "web-acme.example.com"
	rec := httptest.NewRecorder()
	if h.ServeFunnelIfMatched(rec, req) {
		t.Error("ServeFunnelIfMatched = true with no baseDomain configured, want false")
	}
}
