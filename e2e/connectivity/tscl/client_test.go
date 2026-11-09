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
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestValidateConfig(t *testing.T) {
	for _, tt := range []struct {
		name        string
		change      func(*Config)
		interactive bool
		valid       bool
	}{
		{"defaults", func(*Config) {}, false, true},
		{"interactive without key", func(c *Config) { c.AuthKey = "" }, true, true},
		{"interactive with key", func(*Config) {}, true, false},
		{"HTTP control", func(c *Config) { c.ControlURL = "http://localhost:8080" }, false, true},
		{"IPv6 control", func(c *Config) { c.ControlURL = "https://[::1]:443" }, false, true},
		{"empty hostname", func(c *Config) { c.Hostname = " " }, false, false},
		{"empty key", func(c *Config) { c.AuthKey = " " }, false, false},
		{"empty control", func(c *Config) { c.ControlURL = "" }, false, false},
		{"relative control", func(c *Config) { c.ControlURL = "/control" }, false, false},
		{"missing host", func(c *Config) { c.ControlURL = "https://" }, false, false},
		{"invalid scheme", func(c *Config) { c.ControlURL = "ftp://control" }, false, false},
		{"credentials", func(c *Config) { c.ControlURL = "https://user:pass@control" }, false, false},
		{"fragment", func(c *Config) { c.ControlURL = "https://control/#fragment" }, false, false},
		{"malformed URL", func(c *Config) { c.ControlURL = "https://%zz" }, false, false},
		{"startup timeout", func(c *Config) { c.StartupTimeout = -time.Second }, false, false},
		{"probe timeout", func(c *Config) { c.ProbeTimeout = -time.Second }, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Hostname: "test", ControlURL: "https://control.test", AuthKey: "secret"}
			tt.change(&cfg)
			if err := validateConfig(cfg, tt.interactive); (err == nil) != tt.valid {
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
	enroll := func(ctx context.Context, cfg Config) (*Client, error) {
		cfg.AuthKey = "test-key-not-a-real-key"
		return New(ctx, cfg)
	}
	for _, tt := range []struct {
		name     string
		start    func(context.Context, Config) (*Client, error)
		canceled bool
		wantErr  error
	}{
		{"canceled before initialization", enroll, true, context.Canceled},
		{"enrollment timeout", enroll, false, context.DeadlineExceeded},
		{"login canceled before initialization", StartLogin, true, context.Canceled},
		{"login timeout", StartLogin, false, context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			c, err := tt.start(ctx, Config{
				Hostname: "tscl-failure", ControlURL: control.URL,
				StartupTimeout: 250 * time.Millisecond, Logger: discardLogger(),
			})
			if c != nil {
				t.Cleanup(func() { _ = c.Close() })
				t.Fatal("failed enrollment returned a client")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("start() = %v, want %v", err, tt.wantErr)
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
		"logout": func() error { return c.Logout(t.Context()) },
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

func TestParseAddrs(t *testing.T) {
	build := func(rcode dnsmessage.RCode, answers ...dnsmessage.Resource) []byte {
		raw, err := (&dnsmessage.Message{
			Response: true, RCode: rcode,
			Answers: answers,
		}).Pack()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	name := dnsmessage.MustNewName("host.org.os.example.com.")
	hdr := dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET}
	for _, tt := range []struct {
		name  string
		raw   []byte
		want  []string
		valid bool
	}{
		{"a and aaaa", build(dnsmessage.RCodeSuccess,
			dnsmessage.Resource{Header: hdr, Body: &dnsmessage.AResource{A: [4]byte{100, 64, 0, 2}}},
			dnsmessage.Resource{Header: hdr, Body: &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("fd7a:115c:a1e0::2").As16()}},
		), []string{"100.64.0.2", "fd7a:115c:a1e0::2"}, true},
		{"nxdomain", build(dnsmessage.RCodeNameError), nil, true},
		{"garbage", []byte{1, 2, 3}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAddrs(tt.raw)
			if (err == nil) != tt.valid {
				t.Fatalf("parseAddrs() error = %v, valid = %v", err, tt.valid)
			}
			var strs []string
			for _, a := range got {
				strs = append(strs, a.String())
			}
			if !reflect.DeepEqual(strs, tt.want) {
				t.Fatalf("parseAddrs() = %v, want %v", strs, tt.want)
			}
		})
	}
}
