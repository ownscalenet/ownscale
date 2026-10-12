package server

import (
	"bufio"
	"context"
	"io"
	"log/slog"
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
	p := NewSubdomainProxy(http.NotFoundHandler(), "ownscale.test", dial, slog.New(slog.DiscardHandler))

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

// TestSubdomainProxyUpgrade checks that protocol upgrades, which devices use
// for their control connection, pass through the proxy in both directions.
func TestSubdomainProxyUpgrade(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("backend Hijack: %v", err)
			return
		}
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		brw.Flush()
		// Echo one line back.
		line, _ := brw.ReadString('\n')
		brw.WriteString("echo " + line)
		brw.Flush()
	}))
	t.Cleanup(backend.Close)
	dial := func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", backend.Listener.Addr().String())
	}
	front := httptest.NewServer(NewSubdomainProxy(http.NotFoundHandler(), "ownscale.test", dial, slog.New(slog.DiscardHandler)))
	t.Cleanup(front.Close)

	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: aaaa.ownscale.test\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if _, err := io.WriteString(conn, "hello\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := br.ReadString('\n'); err != nil || got != "echo hello\n" {
		t.Errorf("after upgrade got %q, %v, want %q", got, err, "echo hello\n")
	}
}

// TestSubdomainProxyKeyCORS checks that the console origin may fetch
// headscale's /key endpoint, which the in-browser Tailscale client needs.
func TestSubdomainProxyKeyCORS(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pubkey"))
	}))
	t.Cleanup(backend.Close)
	dial := func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", backend.Listener.Addr().String())
	}
	p := NewSubdomainProxy(http.NotFoundHandler(), "ownscale.test", dial, slog.New(slog.DiscardHandler))

	const console = "https://console.ownscale.test"
	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		wantCode   int
		wantOrigin string
	}{
		{"console fetch", http.MethodGet, "/key", console, http.StatusOK, console},
		{"preflight", http.MethodOptions, "/key", console, http.StatusNoContent, console},
		{"other origin", http.MethodGet, "/key", "https://evil.example", http.StatusOK, ""},
		{"no origin", http.MethodGet, "/key", "", http.StatusOK, ""},
		{"other path", http.MethodGet, "/ts2021", console, http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "https://aaaa.ownscale.test"+tt.path, nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tt.wantOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantOrigin)
			}
		})
	}
}
