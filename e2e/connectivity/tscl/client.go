// Package tscl provides ephemeral tsnet machines for connectivity tests.
package tscl

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ownscalenet/ownscale/derrors"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

const (
	defaultStartupTimeout = 30 * time.Second
	defaultProbeTimeout   = 5 * time.Second
)

// Config describes a single machine, enrolled with AuthKey or, for StartLogin,
// interactively.
type Config struct {
	Hostname       string
	ControlURL     string
	AuthKey        string
	Logger         *slog.Logger
	StartupTimeout time.Duration
	ProbeTimeout   time.Duration
}

// Client owns one ephemeral machine. Construct it with New; do not copy it.
type Client struct {
	mu           sync.RWMutex
	server       *tsnet.Server
	dir          string
	identity     string
	probeTimeout time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
	logger       *slog.Logger
	closed       bool
	services     []*HTTPService
	closeOnce    sync.Once
	closeErr     error
}

// New enrolls a machine and waits for assigned addresses. ctx bounds enrollment,
// not the returned client's lifetime; the caller must Close the client.
func New(ctx context.Context, cfg Config) (_ *Client, err error) {
	defer derrors.Wrap(&err, "New()")
	defer func() {
		if err != nil && cfg.AuthKey != "" {
			err = &redactedError{err, cfg.AuthKey}
		}
	}()
	if err = validateConfig(cfg, false); err != nil {
		return nil, err
	}
	ctx, cancel := startupContext(ctx, cfg)
	defer cancel()
	c, err := start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.Close())
		}
	}()
	status, err := c.server.Up(ctx)
	if err != nil {
		return nil, err
	}
	if status.BackendState != "Running" || len(status.TailscaleIPs) == 0 {
		return nil, errors.New("machine is not running with assigned addresses")
	}
	return c, nil
}

// StartLogin starts a machine without an auth key and returns once control
// has given it the URL of an interactive login, which Status reports as
// AuthURL. The login stays pending until the caller closes the client.
// cfg.AuthKey must be empty.
func StartLogin(ctx context.Context, cfg Config) (_ *Client, err error) {
	defer derrors.Wrap(&err, "StartLogin()")
	if err = validateConfig(cfg, true); err != nil {
		return nil, err
	}
	ctx, cancel := startupContext(ctx, cfg)
	defer cancel()
	c, err := start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.Close())
		}
	}()
	lc, err := c.server.LocalClient()
	if err != nil {
		return nil, err
	}
	// tsnet starts the interactive login itself; the URL arrives once control answers.
	for {
		status, err := lc.StatusWithoutPeers(ctx)
		if err == nil && status.AuthURL != "" {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), err)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func startupContext(ctx context.Context, cfg Config) (context.Context, context.CancelFunc) {
	timeout := cfg.StartupTimeout
	if timeout == 0 {
		timeout = defaultStartupTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// start starts a tsnet server for cfg; on failure it cleans up after itself.
func start(ctx context.Context, cfg Config) (_ *Client, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	c := &Client{identity: rand.Text(), probeTimeout: cfg.ProbeTimeout}
	if c.probeTimeout == 0 {
		c.probeTimeout = defaultProbeTimeout
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			err = errors.Join(err, c.Close())
		}
	}()
	c.dir, err = os.MkdirTemp("", "ownscale-tscl-")
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c.logger = logger
	logf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if cfg.AuthKey != "" {
			msg = strings.ReplaceAll(msg, cfg.AuthKey, "[REDACTED]")
		}
		logger.Debug(msg)
	}
	server := &tsnet.Server{
		Dir: c.dir, Hostname: cfg.Hostname, ControlURL: cfg.ControlURL,
		AuthKey: cfg.AuthKey, Ephemeral: true, Logf: logf, UserLogf: logf,
	}
	// Start cleans up its own failures; Close requires successful initialization.
	if err = server.Start(); err != nil {
		return nil, err
	}
	c.server = server
	return c, nil
}

// validateConfig checks cfg; an interactive login must not carry an auth key,
// any other machine must.
func validateConfig(cfg Config, interactive bool) error {
	if strings.TrimSpace(cfg.Hostname) == "" {
		return errors.New("hostname is required")
	}
	if interactive != (strings.TrimSpace(cfg.AuthKey) == "") {
		return errors.New("an auth key is required, except for an interactive login")
	}
	if _, err := parseHTTPURL(cfg.ControlURL); err != nil {
		return errors.New("control URL must be an absolute HTTP(S) URL without credentials or a fragment")
	}
	if cfg.StartupTimeout < 0 || cfg.ProbeTimeout < 0 {
		return errors.New("timeouts must not be negative")
	}
	return nil
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid HTTP(S) URL")
	}
	return u, nil
}

// Status returns the machine's current state, including its tailnet addresses.
func (c *Client) Status(ctx context.Context) (_ *ipnstate.Status, err error) {
	defer derrors.Wrap(&err, "c.Status()")
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, net.ErrClosed
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	lc, err := c.server.LocalClient()
	if err != nil {
		return nil, err
	}
	return lc.Status(ctx)
}

// Logout logs the machine out, so control deletes the ephemeral node now
// rather than after its inactivity timeout.
func (c *Client) Logout(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "c.Logout()")
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return net.ErrClosed
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	lc, err := c.server.LocalClient()
	if err != nil {
		return err
	}
	return lc.Logout(ctx)
}

// DNS returns the DNS configuration the control server currently sends to
// this machine (from its netmap).
func (c *Client) DNS(ctx context.Context) (_ tailcfg.DNSConfig, err error) {
	defer derrors.Wrap(&err, "c.DNS()")
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return tailcfg.DNSConfig{}, net.ErrClosed
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	lc, err := c.server.LocalClient()
	if err != nil {
		return tailcfg.DNSConfig{}, err
	}
	// DNSConfig fails until the first netmap arrives, so retry until ctx ends.
	for {
		cfg, err := lc.DNSConfig(ctx)
		if err == nil && cfg != nil {
			return *cfg, nil
		}
		select {
		case <-ctx.Done():
			return tailcfg.DNSConfig{}, errors.Join(ctx.Err(), err)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// DialContext opens TCP to a tailnet IP:port (not a DNS name). The caller must
// close it and set deadlines for subsequent I/O; ctx only bounds dialing.
func (c *Client) DialContext(ctx context.Context, network, address string) (_ net.Conn, err error) {
	defer derrors.Wrap(&err, "c.DialContext(%q, %q)", network, address)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, net.ErrClosed
	}
	if !tcpNetwork(network) {
		return nil, errors.New("only TCP networks are supported")
	}
	// tsnet can dial public addresses via the host network; disallow that here.
	if err := validateTailnetAddress(address); err != nil {
		return nil, err
	}
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	conn, err := c.server.Dial(ctx, network, address)
	if err != nil {
		return nil, errors.Join(ErrUnreachable, err)
	}
	return conn, nil
}

// Listen opens a tailnet-only TCP listener. The caller must close it.
func (c *Client) Listen(network, address string) (_ net.Listener, err error) {
	defer derrors.Wrap(&err, "c.Listen(%q, %q)", network, address)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, net.ErrClosed
	}
	if !tcpNetwork(network) {
		return nil, errors.New("only TCP networks are supported")
	}
	return c.server.Listen(network, address)
}

func tcpNetwork(network string) bool {
	return network == "tcp" || network == "tcp4" || network == "tcp6"
}

func validateTailnetAddress(address string) error {
	addr, err := netip.ParseAddrPort(address)
	if err != nil || addr.Port() == 0 || addr.Addr().Zone() != "" || !tsaddr.IsTailscaleIP(addr.Addr()) {
		return errors.New("destination must be a tailnet IP and nonzero port")
	}
	return nil
}

func (c *Client) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(c.ctx, cancel)
	if c.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

// Close shuts down services and the machine and removes its temporary state.
// It is idempotent. The underlying tsnet shutdown has no context deadline.
func (c *Client) Close() (err error) {
	defer derrors.Wrap(&err, "c.Close()")
	c.closeOnce.Do(func() {
		c.cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		for _, service := range c.services {
			c.closeErr = errors.Join(c.closeErr, service.Close())
		}
		if c.server != nil {
			c.closeErr = errors.Join(c.closeErr, c.server.Close())
		}
		if c.dir != "" {
			c.closeErr = errors.Join(c.closeErr, os.RemoveAll(c.dir))
		}
	})
	if c.closeErr != nil {
		return errors.Join(ErrCleanup, c.closeErr)
	}
	return c.closeErr
}

type redactedError struct {
	cause  error
	secret string
}

func (e *redactedError) Error() string {
	return strings.ReplaceAll(e.cause.Error(), e.secret, "[REDACTED]")
}

func (e *redactedError) Unwrap() error { return e.cause }
