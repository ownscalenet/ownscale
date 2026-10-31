package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/ownscalenet/ownscale/e2e/connectivity/tscl"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
)

type node interface {
	Status(context.Context) (*ipnstate.Status, error)
	DNS(context.Context) (tailcfg.DNSConfig, error)
	ServeHTTP(uint16) (*tscl.HTTPService, error)
	ProbeHTTP(context.Context, string, string) (tscl.ProbeResult, error)
	Close() error
}

type ownedNode struct {
	client   node
	hostname string
	closed   bool
}

type dispatcher struct {
	nodes    map[string]*ownedNode
	services map[string]*tscl.HTTPService
	requests int
	newNode  func(context.Context, tscl.Config) (node, error)
	newLogin func(context.Context, tscl.Config) (node, error)

	shutdownRequested atomic.Bool
}

func newDispatcher() *dispatcher {
	return &dispatcher{
		nodes: make(map[string]*ownedNode), services: make(map[string]*tscl.HTTPService),
		newNode:  func(ctx context.Context, cfg tscl.Config) (node, error) { return tscl.New(ctx, cfg) },
		newLogin: func(ctx context.Context, cfg tscl.Config) (node, error) { return tscl.StartLogin(ctx, cfg) },
	}
}

// Assign implements jrpc2.Assigner. It always returns a handler (never nil)
// so that unknown methods are rejected by dispatch's own switch as
// "invalid_command", matching the bridge's existing error-code contract
// rather than jrpc2's standard "method not found" response.
func (d *dispatcher) Assign(_ context.Context, method string) jrpc2.Handler {
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		params := json.RawMessage("{}")
		if req.HasParams() {
			params = json.RawMessage(req.ParamString())
		}
		result, err := d.dispatch(ctx, method, params)
		return result, toRPCError(err)
	}
}

type nodeResult struct {
	Node         string   `json:"node"`
	Hostname     string   `json:"hostname"`
	Addresses    []string `json:"addresses"`
	BackendState string   `json:"backendState"`
	// AuthURL is the interactive login a node started by "login" waits on.
	// It changes when control forgets the login, e.g. on a restart.
	AuthURL string `json:"authURL,omitempty"`
}

type dnsRecordResult struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// dnsResult is the DNS configuration a node received from its control server.
type dnsResult struct {
	Node              string            `json:"node"`
	MagicDNS          bool              `json:"magicDns"`
	Domains           []string          `json:"domains"`
	Resolvers         []string          `json:"resolvers"`
	FallbackResolvers []string          `json:"fallbackResolvers"`
	ExtraRecords      []dnsRecordResult `json:"extraRecords"`
}

type serviceResult struct {
	Node      string   `json:"node"`
	Service   string   `json:"service"`
	Identity  string   `json:"identity"`
	Addresses []string `json:"addresses"`
	Port      uint16   `json:"port"`
}

type probeResult struct {
	OK         bool           `json:"ok"`
	StatusCode int            `json:"statusCode"`
	Identity   string         `json:"identity"`
	DurationMs int64          `json:"durationMs"`
	Error      *protocolError `json:"error,omitempty"`
}

func (d *dispatcher) dispatch(parent context.Context, op string, params json.RawMessage) (any, error) {
	if d.requests >= maxRequests {
		return nil, failure("invalid_command")
	}
	d.requests++
	switch op {
	case "hello", "shutdown":
		var args struct {
			Timeout timeout `json:"timeout"`
		}
		if err := decode(params, &args); err != nil {
			return nil, err
		}
		if op == "shutdown" {
			if err := d.close(); err != nil {
				return nil, failure("bridge_failure")
			}
			d.shutdownRequested.Store(true)
			return struct {
				Closed bool `json:"closed"`
			}{true}, nil
		}
		return struct {
			Ready bool `json:"ready"`
		}{true}, nil
	case "create":
		var args struct {
			Timeout    timeout `json:"timeout"`
			Node       string  `json:"node"`
			Hostname   string  `json:"hostname"`
			ControlURL string  `json:"controlURL"`
			AuthKey    string  `json:"authKey"`
		}
		if err := decode(params, &args); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(parent, time.Duration(args.Timeout))
		defer cancel()
		if strings.TrimSpace(args.AuthKey) == "" || len(args.AuthKey) > 4096 {
			return nil, failure("invalid_command")
		}
		if err := d.checkNewNode(args.Node, args.Hostname, args.ControlURL); err != nil {
			return nil, err
		}
		client, err := d.newNode(ctx, tscl.Config{
			Hostname: args.Hostname, ControlURL: args.ControlURL, AuthKey: args.AuthKey,
			StartupTimeout: time.Duration(args.Timeout),
			ProbeTimeout:   5 * time.Second,
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			return nil, err
		}
		d.nodes[args.Node] = &ownedNode{client: client, hostname: args.Hostname}
		result, err := d.status(ctx, args.Node)
		if err != nil {
			d.nodes[args.Node].closed = true
			if closeErr := client.Close(); closeErr != nil {
				return nil, failure("bridge_failure")
			}
			return nil, err
		}
		return result, nil
	case "login":
		var args struct {
			Timeout    timeout `json:"timeout"`
			Node       string  `json:"node"`
			Hostname   string  `json:"hostname"`
			ControlURL string  `json:"controlURL"`
		}
		if err := decode(params, &args); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(parent, time.Duration(args.Timeout))
		defer cancel()
		if err := d.checkNewNode(args.Node, args.Hostname, args.ControlURL); err != nil {
			return nil, err
		}
		client, err := d.newLogin(ctx, tscl.Config{
			Hostname: args.Hostname, ControlURL: args.ControlURL,
			StartupTimeout: time.Duration(args.Timeout),
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			return nil, err
		}
		d.nodes[args.Node] = &ownedNode{client: client, hostname: args.Hostname}
		return d.status(ctx, args.Node)
	case "status", "close", "dns":
		var args struct {
			Timeout timeout `json:"timeout"`
			Node    string  `json:"node"`
		}
		if err := decode(params, &args); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(parent, time.Duration(args.Timeout))
		defer cancel()
		n, err := d.lookup(args.Node)
		if err != nil {
			return nil, err
		}
		if op == "status" {
			return d.status(ctx, args.Node)
		}
		if op == "dns" {
			return d.dns(ctx, args.Node)
		}
		n.closed = true
		if err := n.client.Close(); err != nil {
			return nil, failure("bridge_failure")
		}
		return struct {
			Node   string `json:"node"`
			Closed bool   `json:"closed"`
		}{args.Node, true}, nil
	case "serve":
		var args struct {
			Timeout timeout `json:"timeout"`
			Node    string  `json:"node"`
			Service string  `json:"service"`
			Port    *int    `json:"port"`
		}
		if err := decode(params, &args); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(parent, time.Duration(args.Timeout))
		defer cancel()
		if !handlePattern.MatchString(args.Service) || args.Port == nil || *args.Port < 0 || *args.Port > 65535 {
			return nil, failure("invalid_command")
		}
		n, err := d.lookup(args.Node)
		if err != nil {
			return nil, err
		}
		if d.reserved(args.Service) {
			return nil, failure("duplicate_handle")
		}
		if len(d.services) >= maxServices {
			return nil, failure("invalid_command")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		service, err := n.client.ServeHTTP(uint16(*args.Port))
		if err != nil {
			return nil, err
		}
		d.services[args.Service] = service
		result := serviceResult{Node: args.Node, Service: args.Service, Identity: service.Identity(), Addresses: []string{}}
		for _, addr := range service.Addresses() {
			result.Addresses = append(result.Addresses, addr.String())
			result.Port = addr.Port()
		}
		return result, nil
	case "probe":
		var args struct {
			Timeout          timeout `json:"timeout"`
			Node             string  `json:"node"`
			URL              string  `json:"url"`
			ExpectedIdentity string  `json:"expectedIdentity"`
		}
		if err := decode(params, &args); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(parent, time.Duration(args.Timeout))
		defer cancel()
		n, err := d.lookup(args.Node)
		if err != nil {
			return nil, err
		}
		u, err := url.Parse(args.URL)
		if err != nil || len(args.URL) > 2048 || len(args.ExpectedIdentity) == 0 ||
			len(args.ExpectedIdentity) > 256 || u.Scheme != "http" || u.User != nil ||
			u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
			return nil, failure("invalid_command")
		}
		address := u.Host
		if u.Port() == "" {
			address = net.JoinHostPort(u.Hostname(), "80")
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil || ap.Port() == 0 || ap.Addr().Zone() != "" || !tsaddr.IsTailscaleIP(ap.Addr()) {
			return nil, failure("invalid_command")
		}
		observed, err := n.client.ProbeHTTP(ctx, args.URL, args.ExpectedIdentity)
		result := probeResult{
			OK: err == nil, StatusCode: observed.StatusCode,
			Identity: observed.Identity, DurationMs: observed.Duration.Milliseconds(),
		}
		if err != nil {
			result.Error = classify(err)
		}
		return result, nil
	default:
		return nil, failure("invalid_command")
	}
}

// checkNewNode validates the arguments shared by the ops that start a node.
func (d *dispatcher) checkNewNode(handle, hostname, controlURL string) error {
	u, err := url.Parse(controlURL)
	if !handlePattern.MatchString(handle) || !handlePattern.MatchString(hostname) ||
		len(controlURL) > 2048 || err != nil || u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" ||
		u.RawQuery != "" {
		return failure("invalid_command")
	}
	if d.reserved(handle) {
		return failure("duplicate_handle")
	}
	if len(d.nodes) >= maxNodes {
		return failure("invalid_command")
	}
	return nil
}

func (d *dispatcher) reserved(handle string) bool {
	_, exists := d.nodes[handle]
	_, serviceExists := d.services[handle]
	return exists || serviceExists
}

func (d *dispatcher) lookup(handle string) (*ownedNode, error) {
	if !handlePattern.MatchString(handle) {
		return nil, failure("invalid_command")
	}
	n, ok := d.nodes[handle]
	if !ok {
		return nil, failure("unknown_handle")
	}
	if n.closed {
		return nil, failure("closed_node")
	}
	return n, nil
}

func (d *dispatcher) status(ctx context.Context, handle string) (nodeResult, error) {
	n := d.nodes[handle]
	status, err := n.client.Status(ctx)
	if err != nil {
		return nodeResult{}, err
	}
	if status == nil {
		return nodeResult{}, failure("bridge_failure")
	}
	result := nodeResult{
		Node: handle, Hostname: n.hostname, Addresses: []string{},
		BackendState: status.BackendState, AuthURL: status.AuthURL,
	}
	for _, ip := range status.TailscaleIPs {
		result.Addresses = append(result.Addresses, ip.String())
	}
	return result, nil
}

func (d *dispatcher) dns(ctx context.Context, handle string) (dnsResult, error) {
	cfg, err := d.nodes[handle].client.DNS(ctx)
	if err != nil {
		return dnsResult{}, err
	}
	result := dnsResult{
		Node: handle, MagicDNS: cfg.Proxied, Domains: append([]string{}, cfg.Domains...),
		Resolvers: []string{}, FallbackResolvers: []string{}, ExtraRecords: []dnsRecordResult{},
	}
	for _, r := range cfg.Resolvers {
		result.Resolvers = append(result.Resolvers, r.Addr)
	}
	for _, r := range cfg.FallbackResolvers {
		result.FallbackResolvers = append(result.FallbackResolvers, r.Addr)
	}
	for _, r := range cfg.ExtraRecords {
		result.ExtraRecords = append(result.ExtraRecords, dnsRecordResult{Name: r.Name, Type: r.Type, Value: r.Value})
	}
	return result, nil
}

func (d *dispatcher) close() error {
	var err error
	for _, n := range d.nodes {
		n.closed = true
		err = errors.Join(err, n.client.Close())
	}
	if err != nil {
		return failure("bridge_failure")
	}
	return nil
}

func classify(err error) *protocolError {
	var protocol *protocolError
	var network net.Error
	var operation *net.OpError
	switch {
	case errors.As(err, &protocol):
		return protocol
	case errors.Is(err, tscl.ErrCleanup):
		return failure("bridge_failure")
	case errors.Is(err, context.Canceled):
		return failure("cancelled")
	case errors.Is(err, net.ErrClosed):
		return failure("closed_node")
	case errors.Is(err, context.DeadlineExceeded):
		return failure("timeout")
	case errors.As(err, &network) && network.Timeout():
		return failure("timeout")
	case errors.Is(err, syscall.ECONNREFUSED):
		return failure("refused")
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, tscl.ErrUnreachable):
		return failure("unreachable")
	case errors.Is(err, tscl.ErrInvalidResponse):
		return failure("invalid_response")
	case errors.Is(err, tscl.ErrIdentityMismatch):
		return failure("identity_mismatch")
	case errors.As(err, &operation) && operation.Op == "dial":
		return failure("unreachable")
	default:
		return failure("bridge_failure")
	}
}
