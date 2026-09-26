package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/orgs"
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
		dial:       func(context.Context, string) (net.Conn, error) { return nil, orgs.ErrNotRunning },
	}

	req := httptest.NewRequest("GET", "https://web2-aaaa.ownscale.test/", nil)
	req.Host = "web2-aaaa.ownscale.test"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler was not called for funnel hostname; expected pass-through")
	}
	if rr.Code == http.StatusServiceUnavailable {
		t.Error("got 503; funnel hostname must not hit the org dial path")
	}
}

func TestSubdomainProxyRoutesToOrg(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Host", r.Host)
		_, _ = w.Write([]byte("from org"))
	}))
	t.Cleanup(backend.Close)

	var dialed []string
	dial := func(ctx context.Context, subdomain string) (net.Conn, error) {
		dialed = append(dialed, subdomain)
		if subdomain == "gone" {
			return nil, orgs.ErrNotRunning
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", backend.Listener.Addr().String())
	}
	p := NewSubdomainProxy(http.NotFoundHandler(), "ownscale.test", dial)

	tests := []struct {
		host     string
		wantCode int
		wantBody string
	}{
		{"aaaa.ownscale.test", http.StatusOK, "from org"},
		{"gone.ownscale.test", http.StatusServiceUnavailable, ""},
		{"console.ownscale.test", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "https://"+tt.host+"/", nil)
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if tt.wantBody != "" && rr.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rr.Body.String(), tt.wantBody)
			}
			if tt.wantBody != "" && rr.Header().Get("X-Seen-Host") != tt.host {
				t.Errorf("backend saw Host %q, want %q", rr.Header().Get("X-Seen-Host"), tt.host)
			}
		})
	}
	if len(dialed) != 2 || dialed[0] != "aaaa" || dialed[1] != "gone" {
		t.Errorf("dialed = %v, want [aaaa gone]", dialed)
	}
}
