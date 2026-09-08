package tscl

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestListenerPort(t *testing.T) {
	for _, tt := range []struct {
		address string
		want    uint16
	}{
		{":18080", 18080},
		{"100.64.0.1:18080", 18080},
		{"[fd7a:115c:a1e0::1]:18081", 18081},
		{":0", 0},
		{":65536", 0},
		{":invalid", 0},
		{"invalid", 0},
	} {
		t.Run(tt.address, func(t *testing.T) {
			got, err := listenerPort(tt.address)
			if got != tt.want || (err != nil) != (tt.want == 0) {
				t.Fatalf("listenerPort(%q) = %d, %v; want %d", tt.address, got, err, tt.want)
			}
		})
	}
}

func TestProbeHTTP(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		wantErr string
		wantIs  error
	}{
		{"match", 200, `{"identity":"target"}`, "", nil},
		{"different machine", 200, `{"identity":"other"}`, "identity mismatch", ErrIdentityMismatch},
		{"missing identity", 200, `{}`, "identity mismatch", ErrInvalidResponse},
		{"malformed", 200, `{`, "unexpected end", ErrInvalidResponse},
		{"trailing document", 200, `{"identity":"target"} {}`, "invalid character", ErrInvalidResponse},
		{"wrong type", 200, `{"identity":1}`, "cannot unmarshal", ErrInvalidResponse},
		{"body limit", 200, strings.Repeat("x", maxResponseBytes+1), "size limit", ErrInvalidResponse},
		{"server failure", 503, `unavailable`, "HTTP status: 503", ErrInvalidResponse},
		{"redirect", 302, "", "HTTP status: 302", ErrInvalidResponse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			result, err := probeHTTP(t.Context(), (&net.Dialer{}).DialContext, time.Second, server.URL, "target")
			if (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("probeHTTP() = %+v, %v; want %q", result, err, tt.wantErr)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("probeHTTP() = %v; want typed error %v", err, tt.wantIs)
			}
			if result.StatusCode != tt.status || result.Duration <= 0 {
				t.Fatalf("missing observations: %+v", result)
			}
			if err == nil && result.Identity != "target" {
				t.Fatalf("wrong identity: %+v", result)
			}
		})
	}
}

func TestProbeDeadlines(t *testing.T) {
	for _, tt := range []struct {
		name       string
		flush      bool
		cancel     bool
		probeLimit time.Duration
		want       error
	}{
		{"headers", false, false, 50 * time.Millisecond, context.DeadlineExceeded},
		{"body", true, false, 50 * time.Millisecond, context.DeadlineExceeded},
		{"caller deadline", false, false, time.Minute, context.DeadlineExceeded},
		{"caller cancellation", false, true, time.Minute, context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.flush {
					_, _ = io.WriteString(w, `{"identity":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if tt.cancel {
				cancel()
			}
			_, err := probeHTTP(ctx, (&net.Dialer{}).DialContext, tt.probeLimit, server.URL, "target")
			if !errors.Is(err, tt.want) {
				t.Fatalf("probeHTTP() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestProbeInvalidWireResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "not an HTTP response\r\n\r\n")
	}))
	defer server.Close()
	_, err := probeHTTP(t.Context(), (&net.Dialer{}).DialContext, time.Second, server.URL, "target")
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("probeHTTP() = %v, want invalid response", err)
	}
}

func TestHTTPService(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddrPort(ln.Addr().String())
	service := serveHTTP(ln, "target", []netip.AddrPort{address}, discardLogger())
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	addresses := service.Addresses()
	addresses[0] = netip.AddrPort{}
	if service.Addresses()[0] != address || address.Port() == 0 {
		t.Fatal("endpoint addresses are missing or mutable")
	}
	result, err := probeHTTP(t.Context(), (&net.Dialer{}).DialContext, time.Second, "http://"+address.String(), service.Identity())
	if err != nil || result.Identity != "target" {
		t.Fatalf("probe service = %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPServiceFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := serveHTTP(ln, "target", nil, discardLogger())
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := service.Wait(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener failure not reported: %v", err)
	}
	if err := service.Close(); !errors.Is(err, net.ErrClosed) || !errors.Is(err, ErrCleanup) {
		t.Fatalf("cleanup lost listener failure: %v", err)
	}
}

func TestProbeFreshConnections(t *testing.T) {
	remotes := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remotes <- r.RemoteAddr
		_, _ = io.WriteString(w, `{"identity":"target"}`)
	}))
	defer server.Close()
	// A configured host proxy must not intercept probes.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "100.64.0.1:80" {
			address = server.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	for range 2 {
		if _, err := probeHTTP(t.Context(), dial, time.Second, "http://100.64.0.1", "target"); err != nil {
			t.Fatal(err)
		}
	}
	if first, second := <-remotes, <-remotes; first == second {
		t.Fatalf("connection reused: %s", first)
	}
}

func TestProbeInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name     string
		url      string
		identity string
	}{
		{"relative URL", "/endpoint", "target"},
		{"non HTTP URL", "ftp://100.64.0.1", "target"},
		{"URL credentials", "http://user:pass@100.64.0.1", "target"},
		{"empty expected identity", "http://100.64.0.1", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A nil dialer ensures invalid input is rejected before network I/O.
			if _, err := probeHTTP(t.Context(), nil, time.Second, tt.url, tt.identity); err == nil {
				t.Fatal("invalid probe input accepted")
			}
		})
	}
}

func TestProbeTLSVerification(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"identity":"target"}`)
	}))
	server.Config.ErrorLog = slog.NewLogLogger(discardLogger().Handler(), slog.LevelDebug)
	server.StartTLS()
	defer server.Close()
	if _, err := probeHTTP(t.Context(), (&net.Dialer{}).DialContext, time.Second, server.URL, "target"); err == nil {
		t.Fatal("untrusted TLS certificate was accepted")
	}
}
