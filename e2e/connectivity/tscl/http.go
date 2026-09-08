package tscl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/ownscalenet/ownscale/derrors"
)

const (
	maxResponseBytes = 4096
	shutdownTimeout  = 5 * time.Second
)

// HTTPService serves a client's identity over its tailnet.
type HTTPService struct {
	identity  string
	addresses []netip.AddrPort
	server    *http.Server
	done      chan struct{}
	serveErr  error
	closeOnce sync.Once
	closeErr  error
}

// ServeHTTP starts a tailnet-only identity endpoint at /. Port zero allocates
// a port. Close the returned service or its owning client to stop it.
func (c *Client) ServeHTTP(port uint16) (_ *HTTPService, err error) {
	defer derrors.Wrap(&err, "c.ServeHTTP(%d)", port)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, net.ErrClosed
	}
	ln, err := c.server.Listen("tcp", net.JoinHostPort("", strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	boundPort, err := listenerPort(ln.Addr().String())
	if err != nil {
		return nil, errors.Join(err, ln.Close())
	}
	ip4, ip6 := c.server.TailscaleIPs()
	var addresses []netip.AddrPort
	for _, ip := range []netip.Addr{ip4, ip6} {
		if ip.IsValid() {
			addresses = append(addresses, netip.AddrPortFrom(ip, boundPort))
		}
	}
	if len(addresses) == 0 {
		return nil, errors.Join(errors.New("machine has no tailnet addresses"), ln.Close())
	}
	service := serveHTTP(ln, c.identity, addresses, c.logger)
	c.services = append(c.services, service)
	return service, nil
}

func listenerPort(address string) (uint16, error) {
	// tsnet's wildcard listener reports ":port", without an IP address.
	_, raw, err := net.SplitHostPort(address)
	if err != nil {
		return 0, err
	}
	port, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, err
	}
	if port == 0 {
		return 0, errors.New("listener has no assigned port")
	}
	return uint16(port), nil
}

func serveHTTP(ln net.Listener, identity string, addresses []netip.AddrPort, logger *slog.Logger) *HTTPService {
	body, _ := json.Marshal(struct {
		Identity string `json:"identity"`
	}{identity})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	s := &HTTPService{
		identity: identity, addresses: addresses, done: make(chan struct{}),
		server: &http.Server{
			Handler: mux, ReadHeaderTimeout: defaultProbeTimeout,
			WriteTimeout: defaultProbeTimeout, IdleTimeout: defaultProbeTimeout,
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelDebug),
		},
	}
	go func() {
		defer close(s.done)
		if err := s.server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			s.serveErr = err
		}
	}()
	return s
}

// Identity returns the unique identity shared by this client's HTTP services.
func (s *HTTPService) Identity() string { return s.identity }

// Addresses returns the endpoint's tailnet IPs and allocated port.
func (s *HTTPService) Addresses() []netip.AddrPort {
	return append([]netip.AddrPort(nil), s.addresses...)
}

// Wait waits for serving to stop and reports unexpected listener failures.
func (s *HTTPService) Wait(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "s.Wait()")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.serveErr
	}
}

// Close stops the service, forcing connections closed after five seconds.
// Repeated calls return the same shutdown result.
func (s *HTTPService) Close() (err error) {
	defer derrors.Wrap(&err, "s.Close()")
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		s.closeErr = s.server.Shutdown(ctx)
		s.closeErr = errors.Join(s.closeErr, s.server.Close())
		<-s.done
		s.closeErr = errors.Join(s.closeErr, s.serveErr)
	})
	if s.closeErr != nil {
		return errors.Join(ErrCleanup, s.closeErr)
	}
	return s.closeErr
}

// ProbeResult contains observations from one HTTP request, including partial
// observations when ProbeHTTP returns an error.
type ProbeResult struct {
	StatusCode int
	Identity   string
	Duration   time.Duration
}

// ProbeHTTP makes one fresh-connection request through this node and verifies
// the endpoint identity. A failure does not by itself establish an ACL denial.
func (c *Client) ProbeHTTP(ctx context.Context, targetURL, expectedIdentity string) (_ ProbeResult, err error) {
	defer derrors.Wrap(&err, "c.ProbeHTTP()")
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return ProbeResult{}, net.ErrClosed
	}
	ctx, cancel := c.operationContext(ctx)
	timeout := c.probeTimeout
	c.mu.RUnlock()
	defer cancel()
	return probeHTTP(ctx, c.DialContext, timeout, targetURL, expectedIdentity)
}

func probeHTTP(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), timeout time.Duration, targetURL, expectedIdentity string) (result ProbeResult, err error) {
	start := time.Now()
	defer func() { result.Duration = time.Since(start) }()
	if _, err := parseHTTPURL(targetURL); err != nil {
		return result, err
	}
	if expectedIdentity == "" {
		return result, errors.New("expected identity is required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := &http.Transport{DialContext: dial, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return result, err
	}
	resp, err := client.Do(req)
	if err != nil {
		var network *net.OpError
		if !errors.As(err, &network) && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrUnreachable) {
			return result, errors.Join(ErrInvalidResponse, err)
		}
		return result, err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	result.StatusCode = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("%w: unexpected HTTP status: %d", ErrInvalidResponse, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return result, errors.Join(ErrInvalidResponse, err)
	}
	if len(body) > maxResponseBytes {
		return result, fmt.Errorf("%w: identity response exceeds size limit", ErrInvalidResponse)
	}
	var payload struct {
		Identity string `json:"identity"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return result, errors.Join(ErrInvalidResponse, err)
	}
	result.Identity = payload.Identity
	if result.Identity == "" {
		return result, errors.Join(ErrInvalidResponse, errors.New("endpoint identity mismatch: missing identity"))
	}
	if result.Identity != expectedIdentity {
		return result, ErrIdentityMismatch
	}
	return result, nil
}
