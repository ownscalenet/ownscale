package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/e2e/connectivity/tscl"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
)

type testNode struct {
	closed    bool
	closeErr  error
	probeErr  error
	statusErr error
	dns       tailcfg.DNSConfig
	dnsErr    error
}

func (n *testNode) DNS(context.Context) (_ tailcfg.DNSConfig, err error) {
	defer derrors.Wrap(&err, "n.DNS()")
	return n.dns, n.dnsErr
}

func (n *testNode) Status(context.Context) (_ *ipnstate.Status, err error) {
	defer derrors.Wrap(&err, "n.Status()")
	return &ipnstate.Status{
		BackendState: "Running", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}, n.statusErr
}

func (n *testNode) ServeHTTP(uint16) (_ *tscl.HTTPService, err error) {
	defer derrors.Wrap(&err, "n.ServeHTTP()")
	return nil, errors.New("unexpected service creation")
}

func (n *testNode) ProbeHTTP(context.Context, string, string) (_ tscl.ProbeResult, err error) {
	defer derrors.Wrap(&err, "n.ProbeHTTP()")
	return tscl.ProbeResult{StatusCode: 200, Identity: "other", Duration: 3 * time.Millisecond}, n.probeErr
}

func (n *testNode) Close() (err error) {
	defer derrors.Wrap(&err, "n.Close()")
	n.closed = true
	return n.closeErr
}

func TestDispatchValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		op     string
		params string
		code   string
	}{
		{"hello", "hello", `{"timeout":"5000ms"}`, ""},
		{"zero timeout", "hello", `{"timeout":"0ms"}`, "invalid_command"},
		{"large timeout", "hello", fmt.Sprintf(`{"timeout":"%s"}`, maxTimeout+time.Millisecond), "invalid_command"},
		{"unknown command", "delete", `{"timeout":"5000ms"}`, "invalid_command"},
		{"unknown argument", "hello", `{"timeout":"5000ms","authKey":"secret"}`, "invalid_command"},
		{"duplicate argument", "status", `{"timeout":"5000ms","node":"n","node":"other"}`, "invalid_command"},
		{"null arguments", "hello", `null`, "invalid_command"},
		{"empty arguments", "hello", ` `, "invalid_command"},
		{"unknown node", "status", `{"timeout":"5000ms","node":"absent"}`, "unknown_handle"},
		{"invalid handle", "status", `{"timeout":"5000ms","node":"../secret"}`, "invalid_command"},
		{"missing hostname", "create", `{"timeout":"5000ms","node":"n","controlURL":"http://control","authKey":"secret"}`, "invalid_command"},
		{"missing key", "create", `{"timeout":"5000ms","node":"n","hostname":"host","controlURL":"http://control"}`, "invalid_command"},
		{"control credentials", "create", `{"timeout":"5000ms","node":"n","hostname":"host","controlURL":"******control","authKey":"secret"}`, "invalid_command"},
		{"negative port", "serve", `{"timeout":"5000ms","node":"n","service":"s","port":-1}`, "invalid_command"},
		{"missing port", "serve", `{"timeout":"5000ms","node":"n","service":"s"}`, "invalid_command"},
		{"excess port", "serve", `{"timeout":"5000ms","node":"n","service":"s","port":65536}`, "invalid_command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newDispatcher().dispatch(t.Context(), tt.op, json.RawMessage(tt.params))
			if tt.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || classify(err).Code != tt.code {
				t.Fatalf("error = %v, want %s", err, tt.code)
			}
		})
	}
}

func TestNodeLifecycle(t *testing.T) {
	d := newDispatcher()
	n := &testNode{}
	d.newNode = func(ctx context.Context, cfg tscl.Config) (node, error) {
		if cfg.StartupTimeout != 90*time.Second {
			t.Errorf("startup timeout = %v", cfg.StartupTimeout)
		}
		return n, nil
	}
	create := `{"timeout":"90000ms","node":"n","hostname":"host","controlURL":"https://control","authKey":"secret"}`
	result, err := d.dispatch(t.Context(), "create", json.RawMessage(create))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(nodeResult); got.Node != "n" || got.Hostname != "host" ||
		len(got.Addresses) != 1 || got.Addresses[0] != "100.64.0.1" || got.BackendState != "Running" {
		t.Fatalf("result = %+v", got)
	}
	if _, err := d.dispatch(t.Context(), "create", json.RawMessage(create)); err == nil || classify(err).Code != "duplicate_handle" {
		t.Fatalf("duplicate node = %v", err)
	}
	status := json.RawMessage(`{"timeout":"5000ms","node":"n"}`)
	if _, err := d.dispatch(t.Context(), "status", status); err != nil {
		t.Fatal(err)
	}
	closeReq := json.RawMessage(`{"timeout":"5000ms","node":"n"}`)
	if _, err := d.dispatch(t.Context(), "close", closeReq); err != nil || !n.closed {
		t.Fatalf("close = %v, closed %t", err, n.closed)
	}
	if _, err := d.dispatch(t.Context(), "status", status); err == nil || classify(err).Code != "closed_node" {
		t.Fatalf("closed node = %v", err)
	}
	if _, err := d.dispatch(t.Context(), "create", json.RawMessage(create)); err == nil || classify(err).Code != "duplicate_handle" {
		t.Fatalf("reused handle = %v", err)
	}
}

func TestDNS(t *testing.T) {
	d := newDispatcher()
	d.nodes["n"] = &ownedNode{client: &testNode{dns: tailcfg.DNSConfig{
		Proxied:           true,
		Domains:           []string{"org.ownscale", "internal.example.com"},
		FallbackResolvers: []*dnstype.Resolver{{Addr: "1.1.1.1"}},
		ExtraRecords:      []tailcfg.DNSRecord{{Name: "host.example.com", Type: "A", Value: "10.0.0.1"}},
	}}}
	d.nodes["failing"] = &ownedNode{client: &testNode{dnsErr: context.DeadlineExceeded}}
	d.nodes["gone"] = &ownedNode{client: &testNode{}, closed: true}

	got, err := d.dispatch(t.Context(), "dns", json.RawMessage(`{"timeout":"5000ms","node":"n"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := dnsResult{
		Node: "n", MagicDNS: true, Domains: []string{"org.ownscale", "internal.example.com"},
		Resolvers: []string{}, FallbackResolvers: []string{"1.1.1.1"},
		ExtraRecords: []dnsRecordResult{{Name: "host.example.com", Type: "A", Value: "10.0.0.1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dns = %+v, want %+v", got, want)
	}

	for _, tt := range []struct{ node, code string }{
		{"failing", "timeout"}, {"gone", "closed_node"}, {"absent", "unknown_handle"},
	} {
		_, err := d.dispatch(t.Context(), "dns", json.RawMessage(`{"timeout":"5000ms","node":"`+tt.node+`"}`))
		if err == nil || classify(err).Code != tt.code {
			t.Errorf("dns(%s) error = %v, want %s", tt.node, err, tt.code)
		}
	}
}

func TestCleanupContinuesAndSanitizes(t *testing.T) {
	d := newDispatcher()
	first := &testNode{closeErr: errors.New("authKey=secret")}
	second := &testNode{}
	d.nodes["first"] = &ownedNode{client: first}
	d.nodes["second"] = &ownedNode{client: second}
	_, err := d.dispatch(t.Context(), "shutdown", json.RawMessage(`{"timeout":"5000ms"}`))
	if err == nil || classify(err).Code != "bridge_failure" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("cleanup error = %v", err)
	}
	if !first.closed || !second.closed {
		t.Fatal("cleanup skipped a node")
	}
}

func TestCreateStatusFailureCleansUp(t *testing.T) {
	d := newDispatcher()
	n := &testNode{statusErr: context.DeadlineExceeded}
	d.newNode = func(context.Context, tscl.Config) (node, error) { return n, nil }
	_, err := d.dispatch(t.Context(), "create",
		json.RawMessage(`{"timeout":"5000ms","node":"n","hostname":"host","controlURL":"http://control","authKey":"secret"}`))
	if !errors.Is(err, context.DeadlineExceeded) || !n.closed {
		t.Fatalf("err = %v, closed = %t", err, n.closed)
	}
	if err := d.close(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		code string
	}{
		{"success", nil, ""},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"cancelled", context.Canceled, "cancelled"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "refused"},
		{"unreachable", &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}, "unreachable"},
		{"tsnet unreachable", fmt.Errorf("dial: %w", tscl.ErrUnreachable), "unreachable"},
		{"closed", net.ErrClosed, "closed_node"},
		{"mismatch", tscl.ErrIdentityMismatch, "identity_mismatch"},
		{"invalid response", tscl.ErrInvalidResponse, "invalid_response"},
		{"harness error", errors.New("raw secret"), "bridge_failure"},
		{"cleanup beats cancellation", errors.Join(context.Canceled, tscl.ErrCleanup), "bridge_failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := newDispatcher()
			d.nodes["n"] = &ownedNode{client: &testNode{probeErr: tt.err}}
			got, err := d.dispatch(t.Context(), "probe",
				json.RawMessage(`{"timeout":"5000ms","node":"n","url":"http://100.64.0.1:8080","expectedIdentity":"target"}`))
			if err != nil {
				t.Fatal(err)
			}
			result := got.(probeResult)
			if result.OK != (tt.err == nil) || result.DurationMs != 3 || result.Identity != "other" {
				t.Fatalf("result = %+v", result)
			}
			if tt.code != "" && (result.Error == nil || result.Error.Code != tt.code || strings.Contains(result.Error.Message, "secret")) {
				t.Fatalf("error = %+v, want %s", result.Error, tt.code)
			}
		})
	}
}

func TestProbeRejectsNonTailnetTargets(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:80", "http://example.com:80", "http://100.64.0.1:0",
		"******100.64.0.1:80", "https://100.64.0.1:80",
		"http://100.64.0.1:80/?key=secret", "http://100.64.0.1:80/private",
	} {
		t.Run(target, func(t *testing.T) {
			d := newDispatcher()
			d.nodes["n"] = &ownedNode{client: &testNode{}}
			args, err := json.Marshal(map[string]any{"timeout": "5000ms", "node": "n", "url": target, "expectedIdentity": "target"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = d.dispatch(t.Context(), "probe", args)
			if err == nil || classify(err).Code != "invalid_command" {
				t.Fatalf("probe target = %v", err)
			}
		})
	}
}

func TestCreateOperationDeadline(t *testing.T) {
	d := newDispatcher()
	d.newNode = func(ctx context.Context, cfg tscl.Config) (node, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := d.dispatch(t.Context(), "create",
		json.RawMessage(`{"timeout":"5ms","node":"n","hostname":"host","controlURL":"https://control","authKey":"secret"}`))
	if err == nil || classify(err).Code != "timeout" {
		t.Fatalf("error = %v", err)
	}
	if len(d.nodes) != 0 {
		t.Fatal("unsuccessful creation retained a node")
	}
}

// rpcRequest/rpcReply mirror the standard JSON-RPC envelope the wire
// tests below construct and parse directly, exercising the real Run/run
// entry point end-to-end over newline-delimited frames.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorObject `json:"error,omitempty"`
}

type rpcErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// appCode extracts the application-level protocolError code carried in the
// error's Data field, or "" if there is none (e.g. a transport-level error
// reported directly by the jrpc2 library, such as a JSON parse failure).
func (e *rpcErrorObject) appCode() string {
	if e == nil || len(e.Data) == 0 {
		return ""
	}
	var data protocolError
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return ""
	}
	return data.Code
}

func encodeLine(t *testing.T, w io.Writer, req rpcRequest) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(body, '\n')); err != nil {
		t.Fatal(err)
	}
}

// encodeLineAsync is encodeLine's counterpart for use from a background
// goroutine, where calling (*testing.T).Fatal is not allowed; marshal
// failures here would be a test bug (static literals), so they panic
// instead, and write failures are surfaced by the reading side timing out.
func encodeLineAsync(w io.Writer, req rpcRequest) {
	body, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	_, _ = w.Write(append(body, '\n'))
}
