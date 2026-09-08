package tscl

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestValidateConfig(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Config)
		valid  bool
	}{
		{"defaults", func(*Config) {}, true},
		{"HTTP control", func(c *Config) { c.ControlURL = "http://localhost:8080" }, true},
		{"IPv6 control", func(c *Config) { c.ControlURL = "https://[::1]:443" }, true},
		{"empty hostname", func(c *Config) { c.Hostname = " " }, false},
		{"empty key", func(c *Config) { c.AuthKey = " " }, false},
		{"empty control", func(c *Config) { c.ControlURL = "" }, false},
		{"relative control", func(c *Config) { c.ControlURL = "/control" }, false},
		{"missing host", func(c *Config) { c.ControlURL = "https://" }, false},
		{"invalid scheme", func(c *Config) { c.ControlURL = "ftp://control" }, false},
		{"credentials", func(c *Config) { c.ControlURL = "https://user:pass@control" }, false},
		{"fragment", func(c *Config) { c.ControlURL = "https://control/#fragment" }, false},
		{"malformed URL", func(c *Config) { c.ControlURL = "https://%zz" }, false},
		{"startup timeout", func(c *Config) { c.StartupTimeout = -time.Second }, false},
		{"probe timeout", func(c *Config) { c.ProbeTimeout = -time.Second }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Hostname: "test", ControlURL: "https://control.test", AuthKey: "secret"}
			tt.change(&cfg)
			if err := validateConfig(cfg); (err == nil) != tt.valid {
				t.Fatalf("validateConfig() = %v, valid = %v", err, tt.valid)
			}
		})
	}
}

func TestTailnetAddress(t *testing.T) {
	for _, tt := range []struct {
		addr  string
		valid bool
	}{
		{"100.64.0.1:80", true},
		{"[fd7a:115c:a1e0::1]:8080", true},
		{"127.0.0.1:80", false},
		{"example.com:80", false},
		{"192.168.1.1:80", false},
		{"[::1]:80", false},
		{"[fd7a:115c:a1e0::1%eth0]:80", false},
		{"100.64.0.1", false},
		{"100.64.0.1:0", false},
		{"100.64.0.1:65536", false},
	} {
		t.Run(tt.addr, func(t *testing.T) {
			if err := validateTailnetAddress(tt.addr); (err == nil) != tt.valid {
				t.Fatalf("validateTailnetAddress() = %v, valid = %v", err, tt.valid)
			}
		})
	}
}

func TestUnsupportedNetworking(t *testing.T) {
	c := &Client{}
	for _, network := range []string{"", "udp", "udp4", "udp6", "unix"} {
		t.Run(network, func(t *testing.T) {
			if _, err := c.DialContext(t.Context(), network, "100.64.0.1:80"); err == nil {
				t.Fatal("unsupported dial network accepted")
			}
			if _, err := c.Listen(network, ":80"); err == nil {
				t.Fatal("unsupported listener network accepted")
			}
		})
	}
	if _, err := c.DialContext(t.Context(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("host-network destination accepted")
	}
}

func TestNewFailureCleanup(t *testing.T) {
	// Exercise the real tsnet instance against a local, unavailable controller.
	t.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer control.Close()
	for _, tt := range []struct {
		name     string
		canceled bool
		wantErr  error
	}{
		{"canceled before initialization", true, context.Canceled},
		{"enrollment timeout", false, context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			c, err := New(ctx, Config{
				Hostname: "tscl-failure", ControlURL: control.URL, AuthKey: "test-key-not-a-real-key",
				StartupTimeout: 250 * time.Millisecond, Logger: discardLogger(),
			})
			if c != nil {
				t.Cleanup(func() { _ = c.Close() })
				t.Fatal("failed enrollment returned a client")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() = %v, want %v", err, tt.wantErr)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("state left after failure: %v, %v", entries, err)
			}
		})
	}
}

func TestClose(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), "owned-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, dir: dir}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := serveHTTP(ln, "identity", nil, discardLogger())
	c.services = append(c.services, service)
	opCtx, opCancel := c.operationContext(t.Context())
	defer opCancel()

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := c.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
	}
	wg.Wait()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned state still exists: %v", err)
	}
	if err := service.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("close did not cancel operations")
	}
	for name, operation := range map[string]func() error{
		"status": func() error { _, err := c.Status(t.Context()); return err },
		"dial":   func() error { _, err := c.DialContext(t.Context(), "tcp", "100.64.0.1:80"); return err },
		"listen": func() error { _, err := c.Listen("tcp", ":80"); return err },
		"serve":  func() error { _, err := c.ServeHTTP(0); return err },
		"probe":  func() error { _, err := c.ProbeHTTP(t.Context(), "http://100.64.0.1", "id"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("operation after close = %v", err)
			}
		})
	}
}

func TestRedactedError(t *testing.T) {
	cause := errors.Join(errors.New("auth key secret rejected"), context.Canceled)
	err := &redactedError{cause, "secret"}
	if strings.Contains(err.Error(), "secret") || !errors.Is(err, context.Canceled) {
		t.Fatalf("redaction lost secrecy or error identity: %v", err)
	}
}
