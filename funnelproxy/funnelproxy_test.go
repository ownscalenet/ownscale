package funnelproxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
	"golang.org/x/time/rate"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	"tailscale.com/types/key"
)

// fakePortLookup satisfies PortLookup for tests; returns a fixed port for
// any subdomain.
type fakePortLookup struct{ port int }

func (f *fakePortLookup) LookupPort(_ string) (int, bool) { return f.port, f.port != 0 }

func defaultPortLookup() *fakePortLookup { return &fakePortLookup{port: 19999} }

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
	client *hscl.Client
	err    error
}

func (f *fakeHeadscaleClients) Client(string) (*hscl.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

func TestServeHTTPRouteNotFound(t *testing.T) {
	pool := NewPool(&fakeQueries{routes: map[string]sqlcgen.FunnelRoute{}}, &fakeHeadscaleClients{}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	pool.ServeHTTP(rec, req, "ghost", "acme")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeHTTPQueriesError(t *testing.T) {
	pool := NewPool(&fakeQueries{err: errors.New("boom")}, &fakeHeadscaleClients{}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	pool.ServeHTTP(rec, req, "web", "acme")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestPeerIP(t *testing.T) {
	st := &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): {ID: "1", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
		key.NewNode().Public(): {ID: "2", TailscaleIPs: []netip.Addr{netip.MustParseAddr("fd7a:115c::1"), netip.MustParseAddr("100.64.0.2")}},
		key.NewNode().Public(): {ID: "3"},
	}}
	tests := []struct {
		nodeID  string
		wantIP  string
		wantErr bool
	}{
		{"1", "100.64.0.1", false},
		{"2", "100.64.0.2", false}, // prefers IPv4 even when listed second
		{"3", "", true},
		{"ghost", "", true},
	}
	for _, tt := range tests {
		ip, err := peerIP(st, tt.nodeID)
		if (err != nil) != tt.wantErr {
			t.Errorf("peerIP(%q): err = %v, wantErr %v", tt.nodeID, err, tt.wantErr)
		}
		if ip != tt.wantIP {
			t.Errorf("peerIP(%q) = %q, want %q", tt.nodeID, ip, tt.wantIP)
		}
	}
}

// fakeTunnel stands in for tsnet: node "1" is in the netmap and every dial
// reaches backend.
func fakeTunnel(backend *httptest.Server) *tunnel {
	return &tunnel{
		transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, backend.Listener.Addr().String())
			},
		},
		status: func(context.Context) (*ipnstate.Status, error) {
			return &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
				key.NewNode().Public(): {ID: "1", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
			}}, nil
		},
	}
}

func TestServeHTTP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(backend.Close)

	const maxBody = 1024
	tests := []struct {
		name     string
		targetID string
		asleep   bool
		limits   *orgLimits
		body     io.Reader
		chunked  bool
		want     int
	}{
		{name: "ok", targetID: "1", want: http.StatusOK},
		{name: "body at limit", targetID: "1", body: strings.NewReader(strings.Repeat("a", maxBody)), want: http.StatusOK},
		{name: "asleep org not woken", targetID: "1", asleep: true, want: http.StatusServiceUnavailable},
		{name: "target not in netmap", targetID: "2", want: http.StatusBadGateway},
		{name: "body over limit", targetID: "1", body: strings.NewReader(strings.Repeat("a", maxBody+1)), want: http.StatusRequestEntityTooLarge},
		{name: "chunked body over limit", targetID: "1", body: strings.NewReader(strings.Repeat("a", maxBody+1)), chunked: true, want: http.StatusRequestEntityTooLarge},
		{
			name: "rate limited", targetID: "1",
			limits: &orgLimits{rate: rate.NewLimiter(0, 0), inFlight: make(chan struct{}, 1)},
			want:   http.StatusTooManyRequests,
		},
		{
			name: "too many in flight", targetID: "1",
			limits: &orgLimits{rate: rate.NewLimiter(rate.Inf, 0), inFlight: make(chan struct{})},
			want:   http.StatusServiceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeQueries{routes: map[string]sqlcgen.FunnelRoute{
				"acme/web": {ID: "1", Label: "web", TargetNodeID: tt.targetID, TargetPort: 8080},
			}}
			hs := &fakeHeadscaleClients{}
			if tt.asleep {
				hs.err = errors.New("organization not running")
			}
			pool := NewPool(queries, hs, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), maxBody)
			pool.servers["acme"] = fakeTunnel(backend)
			if tt.limits != nil {
				pool.limits["acme"] = tt.limits
			}

			req := httptest.NewRequest(http.MethodPost, "/", tt.body)
			if tt.chunked {
				req.ContentLength = -1
			}
			rec := httptest.NewRecorder()
			pool.ServeHTTP(rec, req, "web", "acme")
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d, body=%q", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// fakeHeadscale records the headscale user and pre-auth key calls the funnel makes.
type fakeHeadscale struct {
	mu          sync.Mutex
	createdUser bool
	keyBodies   []createKeyBody
	deletedKeys []string
}

type createKeyBody struct {
	User       string    `json:"user"`
	Reusable   bool      `json:"reusable"`
	Expiration time.Time `json:"expiration"`
}

// newFakeUserPreAuthKeyServer builds a fake implementing headscale's user
// and pre-auth key endpoints. existingUsers is the GET /api/v1/user response.
func newFakeUserPreAuthKeyServer(t *testing.T, existingUsers string) (*hscl.Client, *fakeHeadscale) {
	t.Helper()
	f := &fakeHeadscale{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			f.mu.Lock()
			f.createdUser = true
			f.mu.Unlock()
			fmt.Fprint(w, `{"user":{"id":"99","name":"ownscale-funnel"}}`)
			return
		}
		fmt.Fprint(w, existingUsers)
	})
	mux.HandleFunc("/api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodDelete {
			f.deletedKeys = append(f.deletedKeys, r.URL.Query().Get("id"))
			fmt.Fprint(w, `{}`)
			return
		}
		var body createKeyBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode pre-auth key body: %v", err)
		}
		f.keyBodies = append(f.keyBodies, body)
		fmt.Fprint(w, `{"preAuthKey":{"id":"1","key":"fake-key"}}`)
	})
	return hscl.NewUnix(hstest.Socket(t, mux)), f
}

func TestEnsureFunnelAuthKey(t *testing.T) {
	tests := []struct {
		name          string
		users         string
		wantUser      string
		wantNewCreate bool
	}{
		{"no users", `{"users":[]}`, "99", true},
		{"existing funnel user", `{"users":[{"id":"5","name":"ownscale-funnel"}]}`, "5", false},
		{
			"only an OIDC member with the funnel name",
			`{"users":[{"id":"7","name":"ownscale-funnel","providerId":"https://ownscale.test/abc"}]}`,
			"99", true,
		},
		{
			"OIDC member listed before the funnel user",
			`{"users":[{"id":"7","name":"ownscale-funnel","providerId":"https://ownscale.test/abc"},{"id":"5","name":"ownscale-funnel"}]}`,
			"5", false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, f := newFakeUserPreAuthKeyServer(t, tt.users)
			if _, err := ensureFunnelAuthKey(t.Context(), client); err != nil {
				t.Fatalf("ensureFunnelAuthKey: %v", err)
			}
			if f.createdUser != tt.wantNewCreate {
				t.Errorf("created user = %v, want %v", f.createdUser, tt.wantNewCreate)
			}
			if len(f.keyBodies) != 1 {
				t.Fatalf("minted %d keys, want 1", len(f.keyBodies))
			}
			key := f.keyBodies[0]
			if key.User != tt.wantUser {
				t.Errorf("key user = %q, want %q", key.User, tt.wantUser)
			}
			if key.Reusable {
				t.Error("key is reusable, want single-use")
			}
			if until := time.Until(key.Expiration); until <= 0 || until > funnelAuthKeyValidity {
				t.Errorf("key expires in %s, want within %s", until, funnelAuthKeyValidity)
			}
		})
	}
}

// TestGetOrCreateServerConcurrentDedup guards against the race where a
// burst of simultaneous requests for a brand-new funnel each
// mint their own preauth key and start their own tsnet.Server against the
// same on-disk state directory.
func TestGetOrCreateServerConcurrentDedup(t *testing.T) {
	client, f := newFakeUserPreAuthKeyServer(t, `{"users":[]}`)
	pool := NewPool(&fakeQueries{}, &fakeHeadscaleClients{client: client}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)
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
	if got := len(f.keyBodies); got != 1 {
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

	client, f := newFakeUserPreAuthKeyServer(t, `{"users":[]}`)
	pool := NewPool(&fakeQueries{}, &fakeHeadscaleClients{client: client}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)
	pool.upTimeout = 200 * time.Millisecond
	pool.controlURL = func(string) (string, error) { return blockServer.URL, nil }

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
	if len(f.deletedKeys) != 1 || f.deletedKeys[0] != "1" {
		t.Errorf("deleted pre-auth keys = %v, want [1] even though Up failed", f.deletedKeys)
	}
}

func TestEvict(t *testing.T) {
	// A never-answering control server: the tsnet.Servers only need to be
	// started so Evict can close them.
	control := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(control.Close)
	newServer := func() *tsnet.Server {
		srv := &tsnet.Server{Dir: t.TempDir(), ControlURL: control.URL, AuthKey: "tskey-test", Hostname: "evict-test"}
		if err := srv.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { _ = srv.Close() })
		return srv
	}

	pool := NewPool(&fakeQueries{}, &fakeHeadscaleClients{}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)
	pool.servers["aaaa"] = newTunnel(newServer())
	pool.servers["bbbb"] = newTunnel(newServer())

	pool.Evict("aaaa")
	pool.Evict("unknown")

	if _, ok := pool.servers["aaaa"]; ok {
		t.Error("aaaa still cached after Evict")
	}
	if _, ok := pool.servers["bbbb"]; !ok {
		t.Error("bbbb was evicted, want it left alone")
	}
}

func TestForget(t *testing.T) {
	control := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(control.Close)

	pool := NewPool(&fakeQueries{}, &fakeHeadscaleClients{}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)
	for _, sub := range []string{"aaaa", "bbbb"} {
		srv := &tsnet.Server{Dir: pool.stateDir(sub), ControlURL: control.URL, AuthKey: "tskey-test", Hostname: "forget-test"}
		if err := srv.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { _ = srv.Close() })
		pool.servers[sub] = newTunnel(srv)
	}

	if err := pool.Forget("aaaa"); err != nil {
		t.Fatalf("Forget(aaaa): %v", err)
	}
	if err := pool.Forget("unknown"); err != nil {
		t.Errorf("Forget(unknown): %v", err)
	}

	if _, ok := pool.servers["aaaa"]; ok {
		t.Error("aaaa still cached after Forget")
	}
	if _, err := os.Stat(pool.stateDir("aaaa")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("aaaa state dir still exists after Forget: %v", err)
	}
	if _, ok := pool.servers["bbbb"]; !ok {
		t.Error("bbbb was forgotten, want it left alone")
	}
	if _, err := os.Stat(pool.stateDir("bbbb")); err != nil {
		t.Errorf("bbbb state dir: %v", err)
	}
}

func TestServeHTTPReusesConnections(t *testing.T) {
	var newConns atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	backend.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	backend.Start()
	t.Cleanup(backend.Close)

	queries := &fakeQueries{routes: map[string]sqlcgen.FunnelRoute{
		"acme/web": {ID: "1", Label: "web", TargetNodeID: "1", TargetPort: 8080},
	}}
	pool := NewPool(queries, &fakeHeadscaleClients{}, t.TempDir(), slog.New(slog.DiscardHandler), defaultPortLookup(), 1<<20)
	pool.servers["acme"] = fakeTunnel(backend)
	t.Cleanup(pool.Close)

	for i := range 3 {
		rec := httptest.NewRecorder()
		pool.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil), "web", "acme")
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("request %d: status = %d, body = %q", i, rec.Code, rec.Body.String())
		}
	}
	if n := newConns.Load(); n != 1 {
		t.Errorf("backend saw %d connections, want 1 reused", n)
	}
}

// TestRemoveStaleFunnelNodes runs against a real headscale: after cleanup,
// the org's only funnel machine is the running one, named after the funnel.
func TestRemoveStaleFunnelNodes(t *testing.T) {
	type node struct {
		user, name string
		self       bool
	}
	tests := []struct {
		name  string
		nodes []node
		want  []string // remaining machines, as "user/givenName"
	}{
		{
			name:  "only the running funnel",
			nodes: []node{{FunnelHeadscaleUserName, "funnel-aaaa", true}},
			want:  []string{"ownscale-funnel/funnel-aaaa"},
		},
		{
			name: "orphans from lost state",
			nodes: []node{
				{FunnelHeadscaleUserName, "funnel-aaaa", false},
				{FunnelHeadscaleUserName, "funnel-aaaa-1", false},
				{FunnelHeadscaleUserName, "funnel-aaaa-2", true},
			},
			want: []string{"ownscale-funnel/funnel-aaaa"},
		},
		{
			name: "members' machines are kept",
			nodes: []node{
				{"alice", "funnel-aaaa", false},
				{"alice", "laptop", false},
				{FunnelHeadscaleUserName, "funnel-aaaa-1", true},
			},
			want: []string{"alice/funnel-aaaa", "alice/laptop", "ownscale-funnel/funnel-aaaa-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			sock := hstest.Headscale(t)
			client := hscl.NewUnix(sock)
			for _, user := range []string{FunnelHeadscaleUserName, "alice"} {
				if _, err := client.CreateUser(ctx, hscl.CreateUserParams{Name: user}); err != nil {
					t.Fatal(err)
				}
			}
			var selfID string
			for _, n := range tt.nodes {
				registered, err := client.RegisterNode(ctx, n.user, hstest.PendingNode(t, sock, n.user, n.name))
				if err != nil {
					t.Fatalf("RegisterNode(%q): %v", n.name, err)
				}
				if n.self {
					selfID = registered.ID
				}
			}

			if err := removeStaleFunnelNodes(ctx, client, "aaaa", selfID); err != nil {
				t.Fatalf("removeStaleFunnelNodes: %v", err)
			}

			nodes, err := client.ListNodes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, n := range nodes {
				got = append(got, n.User.Name+"/"+n.GivenName)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("machines = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveStaleFunnelNodesListError(t *testing.T) {
	client := hscl.NewUnix(filepath.Join(t.TempDir(), "missing.sock"))
	if err := removeStaleFunnelNodes(t.Context(), client, "aaaa", "1"); err == nil {
		t.Error("removeStaleFunnelNodes with an unreachable headscale: want error")
	}
}
