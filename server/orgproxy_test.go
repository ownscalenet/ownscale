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

func TestOrgProxy(t *testing.T) {
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
	p := newOrgProxy("ownscale.test", dial, slog.New(slog.DiscardHandler))

	tests := []struct {
		subdomain string
		wantCode  int
		wantBody  string
	}{
		{"aaaa", http.StatusOK, "from org"},
		{"gone", http.StatusServiceUnavailable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.subdomain, func(t *testing.T) {
			host := tt.subdomain + ".ownscale.test"
			req := httptest.NewRequest("GET", "https://"+host+"/", nil)
			rr := httptest.NewRecorder()
			p.serve(rr, req, tt.subdomain)

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if tt.wantBody != "" && rr.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rr.Body.String(), tt.wantBody)
			}
			if tt.wantBody != "" && rr.Header().Get("X-Seen-Host") != host {
				t.Errorf("backend saw Host %q, want %q", rr.Header().Get("X-Seen-Host"), host)
			}
		})
	}
	if len(dialed) != 2 || dialed[0] != "aaaa" || dialed[1] != "gone" {
		t.Errorf("dialed = %v, want [aaaa gone]", dialed)
	}
}

// TestOrgProxyUpgrade checks that protocol upgrades, which devices use
// for their control connection, pass through the proxy in both directions.
func TestOrgProxyUpgrade(t *testing.T) {
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
	p := newOrgProxy("ownscale.test", dial, slog.New(slog.DiscardHandler))
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.serve(w, r, "aaaa")
	}))
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

// TestOrgProxyKeyCORS checks that the console origin may fetch
// headscale's /key endpoint, which the in-browser Tailscale client needs.
func TestOrgProxyKeyCORS(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pubkey"))
	}))
	t.Cleanup(backend.Close)
	dial := func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", backend.Listener.Addr().String())
	}
	p := newOrgProxy("ownscale.test", dial, slog.New(slog.DiscardHandler))

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
			p.serve(rr, req, "aaaa")

			if rr.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tt.wantOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantOrigin)
			}
		})
	}
}
