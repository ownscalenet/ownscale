package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractSubdomain(t *testing.T) {
	s := &SubdomainProxy{baseDomain: "ownscale.test"}

	tests := []struct {
		host      string
		wantSub   string
		wantMatch bool
	}{
		{"aaaa.ownscale.test", "aaaa", true},
		{"bbbb.ownscale.test", "bbbb", true},
		{"aaaa.ownscale.test:443", "aaaa", true}, // port stripped
		{"console.ownscale.test", "", false},     // console is reserved
		{"web2-aaaa.ownscale.test", "", false},   // funnel hostname: has hyphen
		{"label-sub.ownscale.test", "", false},   // funnel hostname: has hyphen
		{"ownscale.test", "", false},             // bare base domain
		{"other.example.com", "", false},         // different base domain
		{"a.b.ownscale.test", "", false},         // nested subdomain (dot)
		{"ownscale.test:8080", "", false},        // bare base domain with port
	}

	for _, tt := range tests {
		sub, ok := s.extractSubdomain(tt.host)
		if ok != tt.wantMatch || sub != tt.wantSub {
			t.Errorf("extractSubdomain(%q) = (%q, %v), want (%q, %v)",
				tt.host, sub, ok, tt.wantSub, tt.wantMatch)
		}
	}
}

func TestSubdomainProxyFunnelPassthrough(t *testing.T) {
	// Verify that a funnel hostname is passed through to next instead of
	// returning 503. next records whether it was called.
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	p := &SubdomainProxy{
		next:       next,
		baseDomain: "ownscale.test",
		lookupPort: func(string) (int, bool) { return 0, false },
	}

	req := httptest.NewRequest("GET", "https://web2-aaaa.ownscale.test/", nil)
	req.Host = "web2-aaaa.ownscale.test"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called for funnel hostname; expected pass-through")
	}
	if rr.Code == http.StatusServiceUnavailable {
		t.Error("got 503; funnel hostname must not hit the lookupPort path")
	}
}
