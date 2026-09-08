package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/e2e/connectivity/tscl"
	"tailscale.com/ipn/ipnstate"
)

type testNode struct {
	closed    bool
	closeErr  error
	probeErr  error
	statusErr error
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

func command(op, args string) request {
	return request{Version: version, ID: "request", Op: op, TimeoutMs: 5000, Args: json.RawMessage(args)}
}

func TestDispatchValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  request
		code string
	}{
		{"hello", command("hello", `{}`), ""},
		{"version", request{Version: 2, ID: "request"}, "unsupported_version"},
		{"missing ID", request{Version: 1, Op: "hello", TimeoutMs: 1, Args: json.RawMessage(`{}`)}, "invalid_command"},
		{"zero timeout", request{Version: 1, ID: "request", Op: "hello", Args: json.RawMessage(`{}`)}, "invalid_command"},
		{"large timeout", request{Version: 1, ID: "request", Op: "hello", TimeoutMs: maxTimeoutMs + 1, Args: json.RawMessage(`{}`)}, "invalid_command"},
		{"unknown command", command("delete", `{}`), "invalid_command"},
		{"unknown argument", command("hello", `{"authKey":"secret"}`), "invalid_command"},
		{"duplicate argument", command("status", `{"node":"n","node":"other"}`), "invalid_command"},
		{"null arguments", command("hello", `null`), "invalid_command"},
		{"empty arguments", command("hello", ` `), "invalid_command"},
		{"unknown node", command("status", `{"node":"absent"}`), "unknown_handle"},
		{"invalid handle", command("status", `{"node":"../secret"}`), "invalid_command"},
		{"missing hostname", command("create", `{"node":"n","controlURL":"http://control","authKey":"secret"}`), "invalid_command"},
		{"missing key", command("create", `{"node":"n","hostname":"host","controlURL":"http://control"}`), "invalid_command"},
		{"control credentials", command("create", `{"node":"n","hostname":"host","controlURL":"http://user:secret@control","authKey":"secret"}`), "invalid_command"},
		{"negative port", command("serve", `{"node":"n","service":"s","port":-1}`), "invalid_command"},
		{"missing port", command("serve", `{"node":"n","service":"s"}`), "invalid_command"},
		{"excess port", command("serve", `{"node":"n","service":"s","port":65536}`), "invalid_command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newDispatcher().dispatch(t.Context(), tt.req)
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
	req := command("create", `{"node":"n","hostname":"host","controlURL":"https://control","authKey":"secret"}`)
	req.TimeoutMs = 90000
	result, err := d.dispatch(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(nodeResult); got.Node != "n" || got.Hostname != "host" ||
		len(got.Addresses) != 1 || got.Addresses[0] != "100.64.0.1" || got.BackendState != "Running" {
		t.Fatalf("result = %+v", got)
	}
	req.ID = "duplicate-node"
	if _, err := d.dispatch(t.Context(), req); err == nil || classify(err).Code != "duplicate_handle" {
		t.Fatalf("duplicate node = %v", err)
	}
	req = command("status", `{"node":"n"}`)
	req.ID = "status"
	if _, err := d.dispatch(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := d.dispatch(t.Context(), req); err == nil || classify(err).Code != "invalid_command" {
		t.Fatalf("duplicate ID = %v", err)
	}
	req = command("close", `{"node":"n"}`)
	req.ID = "close"
	if _, err := d.dispatch(t.Context(), req); err != nil || !n.closed {
		t.Fatalf("close = %v, closed %t", err, n.closed)
	}
	req.ID = "closed"
	if _, err := d.dispatch(t.Context(), req); err == nil || classify(err).Code != "closed_node" {
		t.Fatalf("closed node = %v", err)
	}
	req = command("create", `{"node":"n","hostname":"host","controlURL":"https://control","authKey":"secret"}`)
	req.ID = "reused"
	if _, err := d.dispatch(t.Context(), req); err == nil || classify(err).Code != "duplicate_handle" {
		t.Fatalf("reused handle = %v", err)
	}
}

func TestCleanupContinuesAndSanitizes(t *testing.T) {
	d := newDispatcher()
	first := &testNode{closeErr: errors.New("authKey=secret")}
	second := &testNode{}
	d.nodes["first"] = &ownedNode{client: first}
	d.nodes["second"] = &ownedNode{client: second}
	_, err := d.dispatch(t.Context(), command("shutdown", `{}`))
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
	_, err := d.dispatch(t.Context(), command("create", `{"node":"n","hostname":"host","controlURL":"http://control","authKey":"secret"}`))
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
			got, err := d.dispatch(t.Context(), command("probe", `{"node":"n","url":"http://100.64.0.1:8080","expectedIdentity":"target"}`))
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
		"http://user:secret@100.64.0.1:80", "https://100.64.0.1:80",
		"http://100.64.0.1:80/?key=secret", "http://100.64.0.1:80/private",
	} {
		t.Run(target, func(t *testing.T) {
			d := newDispatcher()
			d.nodes["n"] = &ownedNode{client: &testNode{}}
			args, err := json.Marshal(map[string]string{"node": "n", "url": target, "expectedIdentity": "target"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = d.dispatch(t.Context(), command("probe", string(args)))
			if err == nil || classify(err).Code != "invalid_command" {
				t.Fatalf("probe target = %v", err)
			}
		})
	}
}

func TestFraming(t *testing.T) {
	for _, tt := range []struct {
		name string
		line string
		code string
	}{
		{"hello", `{"version":1,"id":"r","op":"hello","timeoutMs":1000,"args":{}}` + "\n", ""},
		{"malformed", `{"version":1,` + "\n", "invalid_command"},
		{"unknown field", `{"version":1,"id":"r","op":"hello","timeoutMs":1000,"args":{},"secret":"do-not-echo"}` + "\n", "invalid_command"},
		{"duplicate field", `{"version":1,"version":1,"id":"r","op":"hello","timeoutMs":1000,"args":{}}` + "\n", "invalid_command"},
		{"wrong field case", `{"Version":1,"id":"r","op":"hello","timeoutMs":1000,"args":{}}` + "\n", "invalid_command"},
		{"trailing document", `{} {}` + "\n", "invalid_command"},
		{"blank", "\n", "invalid_command"},
		{"large", strings.Repeat("x", maxFrameBytes+1) + "\n", "invalid_command"},
		{"partial EOF", `{"version":1,"id":"r","op":"hello","timeoutMs":1000,"args":{}}`, "invalid_command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input, writer := io.Pipe()
			reader, output := io.Pipe()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Run(ctx, input, output); _ = output.Close() }()
			go func() {
				_, _ = io.WriteString(writer, tt.line)
				if tt.name == "partial EOF" {
					_ = writer.Close()
				}
			}()
			var reply response
			if err := json.NewDecoder(reader).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if tt.code == "" {
				if reply.Error != nil || reply.ID != "r" || reply.Result == nil {
					t.Fatalf("reply = %+v", reply)
				}
			} else if reply.Error == nil || reply.Error.Code != tt.code {
				t.Fatalf("reply = %+v, want %s", reply, tt.code)
			}
			_ = writer.Close()
			_ = reader.Close()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("bridge did not stop")
			}
		})
	}
}

func TestCancellationDuringCreate(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(fmt.Sprintf("EOF=%t", eof), func(t *testing.T) {
			d := newDispatcher()
			existing := &testNode{}
			d.nodes["existing"] = &ownedNode{client: existing}
			started := make(chan struct{})
			d.newNode = func(ctx context.Context, _ tscl.Config) (node, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			input, writer := io.Pipe()
			reader, output := io.Pipe()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- run(ctx, input, output, d); _ = output.Close() }()
			req := command("create", `{"node":"n","hostname":"host","controlURL":"https://control","authKey":"secret"}`)
			if err := json.NewEncoder(writer).Encode(req); err != nil {
				t.Fatal(err)
			}
			<-started
			if eof {
				_ = writer.Close()
			} else {
				cancel()
			}
			var reply response
			if err := json.NewDecoder(reader).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.Error == nil || reply.Error.Code != "cancelled" {
				t.Fatalf("reply = %+v", reply)
			}
			select {
			case err := <-done:
				if err != nil || !existing.closed {
					t.Fatalf("run = %v, cleanup = %t", err, existing.closed)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not stop bridge")
			}
			_ = writer.Close()
			_ = reader.Close()
		})
	}
}

func TestSerializedFramesAndShutdown(t *testing.T) {
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, input, output); _ = output.Close() }()
	go func() {
		_, _ = io.WriteString(writer, `{"version":1,"id":"one","op":"hello","timeoutMs":1000,"args":{}}`+"\n"+
			`{"version":1,"id":"two","op":"shutdown","timeoutMs":1000,"args":{}}`+"\n")
	}()
	scanner := bufio.NewScanner(reader)
	for _, id := range []string{"one", "two"} {
		if !scanner.Scan() {
			t.Fatalf("missing %s reply: %v", id, scanner.Err())
		}
		var reply response
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil || reply.ID != id || reply.Error != nil {
			t.Fatalf("reply = %+v, err = %v", reply, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	_ = reader.Close()
}

func TestCreateOperationDeadline(t *testing.T) {
	d := newDispatcher()
	d.newNode = func(ctx context.Context, cfg tscl.Config) (node, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	req := command("create", `{"node":"n","hostname":"host","controlURL":"https://control","authKey":"secret"}`)
	req.TimeoutMs = 5
	_, err := d.dispatch(t.Context(), req)
	if err == nil || classify(err).Code != "timeout" {
		t.Fatalf("error = %v", err)
	}
	if len(d.nodes) != 0 {
		t.Fatal("unsuccessful creation retained a node")
	}
}

func TestIdleCancellation(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, input, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked stdin prevented cancellation")
	}
}

func TestOutputFailureCleansUp(t *testing.T) {
	d := newDispatcher()
	n := &testNode{}
	d.nodes["n"] = &ownedNode{client: n}
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	_ = reader.Close()
	defer writer.Close()
	defer output.Close()
	done := make(chan error, 1)
	go func() { done <- run(t.Context(), input, output, d) }()
	if err := json.NewEncoder(writer).Encode(command("hello", `{}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || classify(err).Code != "bridge_failure" || !n.closed {
			t.Fatalf("run = %v, cleanup = %t", err, n.closed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("output failure prevented cleanup")
	}
}
