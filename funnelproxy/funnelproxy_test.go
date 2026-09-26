package funnelproxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

// fakePortLookup satisfies PortLookup for tests; returns a fixed port for
// any subdomain.
type fakePortLookup struct{ port int }

func (f *fakePortLookup) LookupPort(_ string) (int, bool) { return f.port, f.port != 0 }

func defaultPortLookup() *fakePortLookup { return &fakePortLookup{port: 19999} }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, nil))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestParseHost(t *testing.T) {
	const base = "example.com"
	tests := []struct {
		name          string
		host          string
		wantLabel     string
		wantSubdomain string
		wantOK        bool
	}{
		{"valid funnel host", "web-acme.example.com", "web", "acme", true},
		{"valid with port", "web-acme.example.com:443", "web", "acme", true},
		{"uppercase normalized", "WEB-ACME.example.com", "web", "acme", true},
		{"plain org subdomain, only one label", "acme.example.com", "", "", false},
		{"unrelated domain", "evil.com", "", "", false},
		{"empty label", "-acme.example.com", "", "", false},
		{"empty subdomain", "web-.example.com", "", "", false},
		{"bare base domain", "example.com", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, subdomain, ok := ParseHost(tt.host, base)
			if ok != tt.wantOK || label != tt.wantLabel || subdomain != tt.wantSubdomain {
				t.Errorf("ParseHost(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.host, base, label, subdomain, ok, tt.wantLabel, tt.wantSubdomain, tt.wantOK)
			}
		})
	}
}

// fakeQueries is a funnelproxy.Queries test double backed by an in-memory
// map, avoiding the need for a real sqlite-backed *db.TxQueries in these
// unit tests.
type fakeQueries struct {
	routes map[string]sqlcgen.FunnelRoute // keyed by "subdomain/label"
	err    error
}

func (f *fakeQueries) GetFunnelRouteBySubdomainAndLabel(_ context.Context, arg sqlcgen.GetFunnelRouteBySubdomainAndLabelParams) (sqlcgen.FunnelRoute, error) {
	if f.err != nil {
		return sqlcgen.FunnelRoute{}, f.err
	}
	route, ok := f.routes[arg.Subdomain+"/"+arg.Label]
	if !ok {
		return sqlcgen.FunnelRoute{}, sql.ErrNoRows
	}
	return route, nil
}

// fakeHeadscaleClients is a funnelproxy.HeadscaleClients test double.
type fakeHeadscaleClients struct {
	client *headscaleclient.Client
	err    error
}

func (f *fakeHeadscaleClients) Client(_ context.Context, _ string) (*headscaleclient.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

// newFakeNodeServer builds an httptest.Server implementing headscale's
// GET /api/v1/node endpoint, returning nodes.
func newFakeNodeServer(t *testing.T, nodesJSON string) *headscaleclient.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, nodesJSON)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	return headscaleclient.New(host, "test-api-key")
}

func TestServeHTTPRouteNotFound(t *testing.T) {
	pool := NewPool(&fakeQueries{routes: map[string]sqlcgen.FunnelRoute{}}, &fakeHeadscaleClients{}, t.TempDir(), discardLogger(), defaultPortLookup())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	pool.ServeHTTP(rec, req, "ghost", "acme")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeHTTPQueriesError(t *testing.T) {
	pool := NewPool(&fakeQueries{err: errors.New("boom")}, &fakeHeadscaleClients{}, t.TempDir(), discardLogger(), defaultPortLookup())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	pool.ServeHTTP(rec, req, "web", "acme")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServeHTTPHeadscaleClientError(t *testing.T) {
	queries := &fakeQueries{routes: map[string]sqlcgen.FunnelRoute{
		"acme/web": {ID: "1", Label: "web", TargetNodeID: "1", TargetPort: 8080},
	}}
	pool := NewPool(queries, &fakeHeadscaleClients{err: errors.New("mint failed")}, t.TempDir(), discardLogger(), defaultPortLookup())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	pool.ServeHTTP(rec, req, "web", "acme")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServeHTTPTargetNodeMissing(t *testing.T) {
	queries := &fakeQueries{routes: map[string]sqlcgen.FunnelRoute{
		"acme/web": {ID: "1", Label: "web", TargetNodeID: "does-not-exist", TargetPort: 8080},
	}}
	client := newFakeNodeServer(t, `{"nodes":[{"id":"1","name":"node1","ipAddresses":["100.64.0.1"]}]}`)
	pool := NewPool(queries, &fakeHeadscaleClients{client: client}, t.TempDir(), discardLogger(), defaultPortLookup())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	pool.ServeHTTP(rec, req, "web", "acme")

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestTargetNodeIP(t *testing.T) {
	client := newFakeNodeServer(t, `{"nodes":[
		{"id":"1","name":"ipv4-only","ipAddresses":["100.64.0.1"]},
		{"id":"2","name":"ipv6-and-4","ipAddresses":["fd7a:115c::1","100.64.0.2"]},
		{"id":"3","name":"no-ips","ipAddresses":[]}
	]}`)

	tests := []struct {
		nodeID  string
		wantIP  string
		wantErr bool
	}{
		{"1", "100.64.0.1", false},
		{"2", "100.64.0.2", false}, // prefers the IPv4 address even when listed second
		{"3", "", true},
		{"ghost", "", true},
	}
	for _, tt := range tests {
		ip, err := targetNodeIP(t.Context(), client, tt.nodeID)
		if (err != nil) != tt.wantErr {
			t.Errorf("targetNodeIP(%q): err = %v, wantErr %v", tt.nodeID, err, tt.wantErr)
		}
		if ip != tt.wantIP {
			t.Errorf("targetNodeIP(%q) = %q, want %q", tt.nodeID, ip, tt.wantIP)
		}
	}
}

// newFakeUserPreAuthKeyServer builds an httptest.Server implementing
// headscale's GET/POST /api/v1/user and POST /api/v1/preauthkey endpoints,
// recording every /api/v1/preauthkey request body for assertions.
func newFakeUserPreAuthKeyServer(t *testing.T, existingUsers string) (*headscaleclient.Client, *[]string) {
	t.Helper()
	var preAuthKeyBodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"user":{"id":"99","name":"ownscale-funnel"}}`)
			return
		}
		fmt.Fprint(w, existingUsers)
	})
	mux.HandleFunc("/api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		preAuthKeyBodies = append(preAuthKeyBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"preAuthKey":{"id":"1","key":"fake-key","reusable":true,"ephemeral":false}}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	return headscaleclient.New(host, "test-api-key"), &preAuthKeyBodies
}

// TestGetOrCreateServerConcurrentDedup guards against the race where a
// burst of simultaneous requests for a brand-new funnel each
// mint their own preauth key and start their own tsnet.Server against the
// same on-disk state directory.
func TestGetOrCreateServerConcurrentDedup(t *testing.T) {
	client, preAuthKeyBodies := newFakeUserPreAuthKeyServer(t, `{"users":[]}`)
	pool := NewPool(&fakeQueries{}, &fakeHeadscaleClients{client: client}, t.TempDir(), discardLogger(), defaultPortLookup())
	pool.upTimeout = 2 * time.Second

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	const concurrency = 8
	errs := make([]error, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := range concurrency {
		go func() {
			defer wg.Done()
			_, err := pool.getOrCreateServer(ctx, "acme", client)
			errs[i] = err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Errorf("getOrCreateServer[%d]: got nil error, want a dial/lookup failure (ControlURL is unreachable in this test)", i)
		}
	}
	if got := len(*preAuthKeyBodies); got != 1 {
		t.Errorf("preauthkey minted %d times across %d concurrent callers, want exactly 1 (dedup failed)", got, concurrency)
	}
}

// TestGetOrCreateServerUpTimeout guards against a stuck tsnet registration
// hanging getOrCreateServer.
func TestGetOrCreateServerUpTimeout(t *testing.T) {
	blockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never respond; only unblocks when the client gives up
	}))
	t.Cleanup(blockServer.Close)

	client, _ := newFakeUserPreAuthKeyServer(t, `{"users":[]}`)
	pool := NewPool(&fakeQueries{}, &fakeHeadscaleClients{client: client}, t.TempDir(), discardLogger(), defaultPortLookup())
	pool.upTimeout = 200 * time.Millisecond
	pool.controlURL = func(_ context.Context, _ string) (string, error) { return blockServer.URL, nil }

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err := pool.getOrCreateServer(ctx, "bbbb", client)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("getOrCreateServer: got nil error, want a bounded timeout error (ControlURL never responds)")
	}
	if elapsed > 5*time.Second {
		t.Errorf("getOrCreateServer took %s to return, want well under upTimeout=%s plus overhead (it should not hang indefinitely)", elapsed, pool.upTimeout)
	}
}
