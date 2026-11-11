package orgs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juanfont/headscale/hscontrol"
	"github.com/juanfont/headscale/hscontrol/types"
	"golang.org/x/crypto/bcrypt"
	"tailscale.com/tailcfg"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// testFixedSubdomains is the fixedSubdomains list used across orgs package
// tests.
var testFixedSubdomains = []string{
	"aaaa", "bbbb", "cccc", "dddd", "eeee",
	"ffff", "gggg", "hhhh", "iiii", "jjjj",
}

// newRealService builds a Service backed by a fresh database that starts
// real headscale instances.
func newRealService(t *testing.T) *Service {
	t.Helper()
	_, queries := dbtest.New(t)
	cfg := &config.Config{
		FastOrgs:        true,
		BaseDomain:      "example.com",
		DataDir:         t.TempDir(),
		FixedSubdomains: testFixedSubdomains,
		ContentDomain:   "example.com",
	}
	svc, err := NewService(queries, cfg, "https://auth.example.com", slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.derpMap.Store(testDERPMap(1))
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

// testDERPMap returns a DERP map with one region, regionID.
func testDERPMap(regionID int) *tailcfg.DERPMap {
	id := tailcfg.DERPRegionID(regionID)
	return &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{
		id: {RegionID: id, RegionCode: fmt.Sprintf("r%d", regionID), Nodes: []*tailcfg.DERPNode{
			{Name: fmt.Sprintf("%da", regionID), RegionID: id, HostName: "derp.example.com"},
		}},
	}}
}

// newTestService is newRealService, but never starts headscale.
func newTestService(t *testing.T) *Service {
	t.Helper()
	svc := newRealService(t)
	svc.createFn = noopCreateFn
	return svc
}

// noopCreateFn builds no instance, but leaves the database a headscale start
// would create.
func noopCreateFn(p headscaleParams) (*hscontrol.Headscale, error) {
	path := sqlitePath(p.dataDir, p.subdomain)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return nil, os.WriteFile(path, nil, 0o600)
}

// createStarted creates organization subdomain on svc, which starts its
// headscale, and stops that headscale when the test ends.
func createStarted(t *testing.T, svc *Service, subdomain string) sqlcgen.Organization {
	t.Helper()
	org, err := svc.Create(t.Context(), subdomain)
	if err != nil {
		t.Fatalf("Create(%q): %v", subdomain, err)
	}
	t.Cleanup(func() {
		if inst, ok := svc.running(subdomain); ok {
			inst.cancel()
			<-inst.exited
		}
	})
	return org
}

// writeDatabase writes the headscale database an earlier start of
// subdomain would have left in svc's data directory.
func writeDatabase(t *testing.T, svc *Service, subdomain string) {
	t.Helper()
	path := sqlitePath(svc.cfg.DataDir, subdomain)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// failingCreateFn returns a createFn that fails, counting its calls in n.
func failingCreateFn(n *int) func(headscaleParams) (*hscontrol.Headscale, error) {
	return func(headscaleParams) (*hscontrol.Headscale, error) {
		*n++
		return nil, errors.New("headscale failed (injected)")
	}
}

// TestCreate verifies that Create stores an organization with its OIDC
// client and a HeadscaleConfig holding the client's credentials, and starts
// its headscale.
func TestCreate(t *testing.T) {
	svc := newTestService(t)
	var starts int
	svc.createFn = func(p headscaleParams) (*hscontrol.Headscale, error) {
		starts++
		return noopCreateFn(p)
	}

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if org.Subdomain != "acme" {
		t.Errorf("org = %+v, want acme", org)
	}
	if starts != 1 {
		t.Errorf("headscale started %d times, want 1", starts)
	}
	if !svc.hasDatabase("acme") {
		t.Error("no headscale database after Create")
	}

	client, err := svc.queries.GetOIDCClientByOrganizationID(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOIDCClientByOrganizationID: %v", err)
	}
	var redirectURIs []string
	if err := json.Unmarshal([]byte(client.RedirectUris), &redirectURIs); err != nil {
		t.Fatalf("Unmarshal redirect uris: %v", err)
	}
	wantRedirects := []string{
		"https://acme.example.com/oidc/callback",
		"https://acme.example.com/admin/oidc/callback",
	}
	if !slices.Equal(redirectURIs, wantRedirects) {
		t.Errorf("redirect uris = %v, want %v", redirectURIs, wantRedirects)
	}

	row, err := svc.queries.GetOrgConfig(t.Context(), sqlcgen.GetOrgConfigParams{
		OrganizationID: org.ID,
		Path:           HeadscaleConfigKey,
	})
	if err != nil {
		t.Fatalf("GetOrgConfig: %v", err)
	}
	stored, err := parseHeadscaleConfig(row.Content)
	if err != nil {
		t.Fatalf("parseHeadscaleConfig: %v", err)
	}
	if stored.OIDC.Issuer != "https://auth.example.com" || stored.OIDC.ClientID != client.ClientID {
		t.Errorf("stored OIDC = %+v, want issuer https://auth.example.com and client id %q", stored.OIDC, client.ClientID)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecretHash), []byte(stored.OIDC.ClientSecret)); err != nil {
		t.Errorf("stored client secret doesn't match the client's hash: %v", err)
	}
}

// TestCreateDeletesOrgWhenHeadscaleFails verifies an organization whose
// headscale doesn't start isn't kept.
func TestCreateDeletesOrgWhenHeadscaleFails(t *testing.T) {
	svc := newTestService(t)
	var starts int
	svc.createFn = failingCreateFn(&starts)

	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("Create: want the start failure, got nil")
	}
	if _, err := svc.queries.GetOrganizationBySubdomain(t.Context(), "acme"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetOrganizationBySubdomain: err = %v, want sql.ErrNoRows", err)
	}
}

// TestEnsureRefusesMissingDatabase verifies Ensure doesn't start a fresh
// headscale for an organization whose database is gone.
func TestEnsureRefusesMissingDatabase(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Remove(sqlitePath(svc.cfg.DataDir, "acme")); err != nil {
		t.Fatal(err)
	}
	var starts int
	svc.createFn = failingCreateFn(&starts)

	if _, err := svc.Ensure(t.Context(), "acme"); !errors.Is(err, ErrDatabaseMissing) {
		t.Errorf("Ensure: err = %v, want ErrDatabaseMissing", err)
	}
	if starts != 0 {
		t.Errorf("headscale started %d times, want 0", starts)
	}
	if got := svc.Status("acme"); got != StatusUnavailable {
		t.Errorf("Status = %q, want %q", got, StatusUnavailable)
	}
}

// TestCreateRefusesLeftoverDataDirectory verifies a new organization never
// gets the headscale database a deleted one left behind.
func TestCreateRefusesLeftoverDataDirectory(t *testing.T) {
	svc := newTestService(t)
	writeDatabase(t, svc, "acme")

	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("Create: want an error for the leftover data directory, got nil")
	}
	if _, err := svc.queries.GetOrganizationBySubdomain(t.Context(), "acme"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetOrganizationBySubdomain: err = %v, want sql.ErrNoRows", err)
	}
}

func TestCreateRejectsInvalidSubdomain(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.Create(t.Context(), "INVALID!"); err == nil {
		t.Fatal("Create with invalid subdomain: want error, got nil")
	}
}

// TestEnsureBacksOffAfterFailedStart verifies a failed start is reported,
// and that Ensure then fails fast instead of starting again until
// startRetryDelay has passed.
func TestEnsureBacksOffAfterFailedStart(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var starts int
	svc.createFn = failingCreateFn(&starts)

	if _, err := svc.Ensure(t.Context(), "acme"); err == nil || errors.Is(err, ErrNotRunning) {
		t.Fatalf("first Ensure: err = %v, want the start failure", err)
	}
	if _, err := svc.Ensure(t.Context(), "acme"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Ensure within the retry delay: err = %v, want ErrNotRunning", err)
	}
	if starts != 1 {
		t.Errorf("starts within the retry delay = %d, want 1", starts)
	}

	svc.startFailures.Store("acme", time.Now().Add(-startRetryDelay))
	if _, err := svc.Ensure(t.Context(), "acme"); err == nil {
		t.Error("Ensure after the retry delay: want the start failure, got nil")
	}
	if starts != 2 {
		t.Errorf("starts after the retry delay = %d, want 2", starts)
	}
}

// TestEnsureStartsOnce verifies concurrent Ensure calls on a sleeping
// organization start one headscale and all get its client.
func TestEnsureStartsOnce(t *testing.T) {
	svc := newRealService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.sleepHeadscale(t.Context(), "acme"); err != nil {
		t.Fatalf("sleepHeadscale: %v", err)
	}
	var starts atomic.Int32
	svc.createFn = func(p headscaleParams) (*hscontrol.Headscale, error) {
		starts.Add(1)
		return createHeadscale(p)
	}
	t.Cleanup(func() {
		if inst, ok := svc.running("acme"); ok {
			inst.cancel()
			<-inst.exited
		}
	})

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { _, errs[i] = svc.Ensure(t.Context(), "acme") })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Ensure #%d: %v", i, err)
		}
	}
	if n := starts.Load(); n != 1 {
		t.Errorf("headscale started %d times, want 1", n)
	}
	if got := svc.Status("acme"); got != StatusActive {
		t.Errorf("Status = %q, want %q", got, StatusActive)
	}
}

// TestDialOrgStartsOrganization verifies device traffic reaches an
// organization, and is refused for an unknown subdomain.
func TestDialOrgStartsOrganization(t *testing.T) {
	svc := newRealService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		if inst, ok := svc.running("acme"); ok {
			inst.cancel()
			<-inst.exited
		}
	})

	tests := []struct {
		subdomain string
		wantErr   error
	}{
		{"acme", nil},
		{"unknown", ErrNotRunning},
	}
	for _, tt := range tests {
		t.Run(tt.subdomain, func(t *testing.T) {
			conn, err := svc.DialOrg(t.Context(), tt.subdomain)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("DialOrg: err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DialOrg: %v", err)
			}
			conn.Close()
			if got := svc.Status(tt.subdomain); got != StatusActive {
				t.Errorf("Status = %q, want %q", got, StatusActive)
			}
		})
	}
}

// TestCreateRejectsDuplicateSubdomain verifies a duplicate Create fails
// without disturbing the existing organization or its headscale.
func TestCreateRejectsDuplicateSubdomain(t *testing.T) {
	svc := newTestService(t)

	first, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	svc.createFn = func(headscaleParams) (*hscontrol.Headscale, error) {
		t.Error("headscale started for a duplicate organization")
		return nil, nil
	}
	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("second Create with duplicate subdomain: want error, got nil")
	}
	org, err := svc.queries.GetOrganizationBySubdomain(t.Context(), "acme")
	if err != nil {
		t.Fatalf("GetOrganizationBySubdomain: %v", err)
	}
	if org.ID != first.ID {
		t.Errorf("organization ID = %q, want the first one %q", org.ID, first.ID)
	}
}

// TestCreateMaxOrgs verifies Create refuses a new organization once
// config.Config.MaxOrgs exist, without starting its headscale.
func TestCreateMaxOrgs(t *testing.T) {
	tests := []struct {
		name     string
		maxOrgs  int
		existing int
		wantErr  error
	}{
		{name: "no limit", maxOrgs: 0, existing: 3},
		{name: "under the limit", maxOrgs: 2, existing: 1},
		{name: "at the limit", maxOrgs: 2, existing: 2, wantErr: ErrMaxOrgs},
		{name: "single organization", maxOrgs: 1, existing: 1, wantErr: ErrMaxOrgs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			for i := range tt.existing {
				if _, err := svc.Create(t.Context(), fmt.Sprintf("org%d", i)); err != nil {
					t.Fatalf("Create existing: %v", err)
				}
			}
			svc.cfg.MaxOrgs = tt.maxOrgs
			var starts int
			svc.createFn = func(p headscaleParams) (*hscontrol.Headscale, error) {
				starts++
				return noopCreateFn(p)
			}

			_, err := svc.Create(t.Context(), "newone")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create: err = %v, want %v", err, tt.wantErr)
			}
			n, err := svc.queries.CountOrganizations(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantCount, wantStarts := int64(tt.existing+1), 1
			if tt.wantErr != nil {
				wantCount, wantStarts = int64(tt.existing), 0
			}
			if n != wantCount {
				t.Errorf("organizations = %d, want %d", n, wantCount)
			}
			if starts != wantStarts {
				t.Errorf("headscale started %d times, want %d", starts, wantStarts)
			}
		})
	}
}

// TestCreateMaxOrgsConcurrent verifies concurrent Creates don't go over
// config.Config.MaxOrgs.
func TestCreateMaxOrgsConcurrent(t *testing.T) {
	svc := newTestService(t)
	svc.cfg.MaxOrgs = 1

	const n = 8
	var wg sync.WaitGroup
	var created, refused atomic.Int32
	for i := range n {
		wg.Go(func() {
			_, err := svc.Create(t.Context(), fmt.Sprintf("org%d", i))
			switch {
			case err == nil:
				created.Add(1)
			case errors.Is(err, ErrMaxOrgs):
				refused.Add(1)
			default:
				t.Errorf("Create: %v", err)
			}
		})
	}
	wg.Wait()

	if created.Load() != 1 || refused.Load() != n-1 {
		t.Errorf("created %d, refused %d; want 1 and %d", created.Load(), refused.Load(), n-1)
	}
	count, err := svc.queries.CountOrganizations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("organizations = %d, want 1", count)
	}
}

func TestNextSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		hint  string
		taken []string
		// leftover subdomains have a data directory, but no organization.
		leftover []string
		want     string // "" means a random subdomain
	}{
		{"fixed subdomain named after the user", "cccc", nil, nil, "cccc"},
		{"no hint gets a random subdomain", "", nil, nil, ""},
		{"unknown hint gets a random subdomain", "acme", nil, nil, ""},
		{"taken fixed subdomain", "aaaa", []string{"aaaa"}, nil, ""},
		{"leftover data directory", "bbbb", nil, []string{"bbbb"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			for _, sub := range tt.taken {
				if _, err := svc.Create(t.Context(), sub); err != nil {
					t.Fatalf("Create(%q): %v", sub, err)
				}
			}
			for _, sub := range tt.leftover {
				writeDatabase(t, svc, sub)
			}
			got, err := svc.NextSubdomain(t.Context(), tt.hint)
			if err != nil {
				t.Fatalf("NextSubdomain: %v", err)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("NextSubdomain = %q, want %q", got, tt.want)
			}
			if tt.want == "" {
				if slices.Contains(testFixedSubdomains, got) {
					t.Errorf("NextSubdomain = %q, want a random subdomain", got)
				}
				if err := ValidateSubdomain(got); err != nil {
					t.Errorf("random subdomain %q is invalid: %v", got, err)
				}
			}
		})
	}
}

// blockingCreateFn returns a createFn that signals entered and then blocks
// until release is closed, building no instance.
func blockingCreateFn(entered chan<- struct{}, release <-chan struct{}) func(headscaleParams) (*hscontrol.Headscale, error) {
	return func(headscaleParams) (*hscontrol.Headscale, error) {
		entered <- struct{}{}
		<-release
		return nil, nil
	}
}

// runBlocked runs f in the background with svc's createFn blocked, and
// returns once f reached createFn. f is released and awaited on cleanup.
func runBlocked(t *testing.T, svc *Service, f func()) {
	t.Helper()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	svc.createFn = blockingCreateFn(entered, release)
	go func() {
		defer close(done)
		f()
	}()
	<-entered
	t.Cleanup(func() {
		close(release)
		<-done
	})
}

// serving stores a running instance for subdomain on port in svc, exited
// already if exited is set, and returns its runtime.
func serving(svc *Service, subdomain string, port int, exited bool) *orgRuntime {
	inst := &instance{exited: make(chan struct{})}
	var once sync.Once
	inst.cancel = func() { once.Do(func() { close(inst.exited) }) }
	if exited {
		inst.cancel()
	}
	rt := &orgRuntime{port: port}
	rt.inst.Store(inst)
	rt.touch()
	svc.orgs.Store(subdomain, rt)
	return rt
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name string
		// setup prepares svc and returns the subdomain to check.
		setup func(t *testing.T, svc *Service) string
		want  string
	}{
		{
			name:  "serving instance",
			setup: func(_ *testing.T, svc *Service) string { serving(svc, "acme", 1, false); return "acme" },
			want:  StatusActive,
		},
		{
			name:  "exited instance",
			setup: func(_ *testing.T, svc *Service) string { serving(svc, "acme", 1, true); return "acme" },
			want:  StatusUnavailable,
		},
		{
			name:  "database missing",
			setup: func(_ *testing.T, svc *Service) string { return "acme" },
			want:  StatusUnavailable,
		},
		{
			name: "started before this process",
			setup: func(t *testing.T, svc *Service) string {
				writeDatabase(t, svc, "acme")
				return "acme"
			},
			want: StatusSleeping,
		},
		{
			name: "put to sleep",
			setup: func(t *testing.T, svc *Service) string {
				serving(svc, "acme", 1, false)
				writeDatabase(t, svc, "acme")
				if err := svc.sleepHeadscale(t.Context(), "acme"); err != nil {
					t.Fatalf("sleepHeadscale: %v", err)
				}
				return "acme"
			},
			want: StatusSleeping,
		},
		{
			name: "failed to start",
			setup: func(t *testing.T, svc *Service) string {
				if _, err := svc.Create(t.Context(), "acme"); err != nil {
					t.Fatalf("Create: %v", err)
				}
				var starts int
				svc.createFn = failingCreateFn(&starts)
				_, _ = svc.Ensure(t.Context(), "acme")
				return "acme"
			},
			want: StatusUnavailable,
		},
		{
			name: "restart in flight",
			setup: func(t *testing.T, svc *Service) string {
				createStarted(t, svc, "acme")
				runBlocked(t, svc, func() { _ = svc.restartHeadscale(t.Context(), "acme") })
				return "acme"
			},
			want: StatusUpdatePending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			subdomain := tt.setup(t, svc)
			if got := svc.Status(subdomain); got != tt.want {
				t.Errorf("Status(%q) = %q, want %q", subdomain, got, tt.want)
			}
		})
	}
}

// TestRestartClosesOrgConnections verifies that restarting an org closes the
// connections handed out by DialOrg (and only that org's), so devices
// reconnect to the new instance instead of staying on the stopped one.
func TestRestartClosesOrgConnections(t *testing.T) {
	svc := newTestService(t)
	for _, sub := range []string{"acme", "other"} {
		if _, err := svc.Create(t.Context(), sub); err != nil {
			t.Fatalf("Create(%q): %v", sub, err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	// Both orgs "run" on the fake listener.
	serving(svc, "acme", port, false)
	serving(svc, "other", port, false)

	acme, err := svc.DialOrg(t.Context(), "acme")
	if err != nil {
		t.Fatalf("DialOrg(acme): %v", err)
	}
	other, err := svc.DialOrg(t.Context(), "other")
	if err != nil {
		t.Fatalf("DialOrg(other): %v", err)
	}
	if _, err := svc.DialOrg(t.Context(), "missing"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("DialOrg(missing) error = %v, want ErrNotRunning", err)
	}

	if err := svc.restartHeadscale(t.Context(), "acme"); err != nil {
		t.Fatalf("restartHeadscale: %v", err)
	}

	_ = acme.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := acme.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("acme connection after restart: Read error = %v, want net.ErrClosed", err)
	}
	_ = other.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := other.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("other connection: Read error = %v, want a timeout (still open)", err)
	}
}

// TestStartRetriesTakenPort verifies that when another process holds an
// org's port, its headscale starts on a new port instead of failing.
func TestStartRetriesTakenPort(t *testing.T) {
	svc := newRealService(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	taken := ln.Addr().(*net.TCPAddr).Port
	svc.orgs.Store("acme", &orgRuntime{port: taken})
	org := dbtest.CreateOrg(t, svc.queries.Queries, "acme")

	svc.orgLock("acme").Lock()
	err = svc.startLocked(t.Context(), org.ID, "acme", newHeadscaleConfig(OIDCConfig{Issuer: "https://auth.example.com"}))
	svc.orgLock("acme").Unlock()
	if err != nil {
		t.Fatalf("startLocked: %v", err)
	}
	inst, ok := svc.running("acme")
	if !ok {
		t.Fatal("acme not running after startLocked")
	}
	t.Cleanup(func() { inst.cancel(); <-inst.exited })

	if port, _ := svc.LookupPort("acme"); port == taken {
		t.Errorf("port = %d, want a port other than the taken one", port)
	}
	if _, err := inst.client.ListUsers(t.Context()); err != nil {
		t.Errorf("ListUsers: %v", err)
	}
}

// testHeadscaleParams returns createHeadscale params for organization acme
// with node certificates from certs, if non-nil.
func testHeadscaleParams(t *testing.T, certs *nodeCerts) headscaleParams {
	hsCfg := newHeadscaleConfig(OIDCConfig{Issuer: "https://auth.example.com"})
	hsCfg.DNS.BaseDomain = "netacme.os.example.com"
	return headscaleParams{
		dataDir:    t.TempDir(),
		subdomain:  "acme",
		baseDomain: "example.com",
		hsCfg:      hsCfg,
		derpMap:    testDERPMap(7),
		certs:      certs,
	}
}

func TestCreateHeadscaleDERP(t *testing.T) {
	app, err := createHeadscale(testHeadscaleParams(t, nil))
	if err != nil {
		t.Fatalf("createHeadscale: %v", err)
	}
	t.Cleanup(func() { _ = app.GetState().Close() })

	derpCfg := app.GetState().DebugConfig().DERP
	if derpCfg.AutoUpdate || len(derpCfg.URLs) != 0 {
		t.Errorf("DERP config fetches by itself: %+v", derpCfg)
	}
	if derpCfg.DERPMap == nil || derpCfg.DERPMap.Regions[7] == nil {
		t.Errorf("DERPMap = %+v, want region 7", derpCfg.DERPMap)
	}
}

// TestCreateHeadscaleBaseDomain guards against nodes getting bare hostnames
// as their FQDN, which breaks MagicDNS resolution of <host>.<base_domain>.
func TestCreateHeadscaleBaseDomain(t *testing.T) {
	app, err := createHeadscale(testHeadscaleParams(t, nil))
	if err != nil {
		t.Fatalf("createHeadscale: %v", err)
	}
	t.Cleanup(func() { _ = app.GetState().Close() })

	if got, want := app.GetState().DebugConfig().BaseDomain, "netacme.os.example.com"; got != want {
		t.Errorf("BaseDomain = %q, want %q", got, want)
	}
}

func TestSetOIDCIdentity(t *testing.T) {
	svc := newRealService(t)
	createStarted(t, svc, "acme")
	inst, _ := svc.running("acme")
	state := inst.app.GetState()

	newUser := func(name string) string {
		u, _, err := state.CreateUser(types.User{Name: name, Email: name + "@example.com", DisplayName: name})
		if err != nil {
			t.Fatalf("CreateUser(%q): %v", name, err)
		}
		return strconv.FormatUint(uint64(u.ID), 10)
	}
	alice, bob := newUser("alice"), newUser("bob")
	identifier := (&types.OIDCClaims{Iss: "https://auth.example.com", Sub: "sub-1"}).Identifier()
	if got := svc.Identifier("sub-1"); got != identifier {
		t.Errorf("Identifier(sub-1) = %q, want %q", got, identifier)
	}

	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-1"); err != nil {
		t.Fatalf("SetOIDCIdentity: %v", err)
	}
	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-1"); err != nil {
		t.Errorf("SetOIDCIdentity of alice's own identity: %v, want nil", err)
	}
	got, err := state.GetUserByOIDCIdentifier(identifier)
	if err != nil {
		t.Fatalf("GetUserByOIDCIdentifier: %v", err)
	}
	if strconv.FormatUint(uint64(got.ID), 10) != alice || got.Name != "alice" || got.Email != "alice@example.com" || got.Provider != "oidc" {
		t.Errorf("user = %+v, want alice unchanged apart from the OIDC identity", got)
	}

	// A stale identity is replaced by the new owner's.
	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-2"); err != nil {
		t.Fatalf("SetOIDCIdentity again: %v", err)
	}
	identifier2 := (&types.OIDCClaims{Iss: "https://auth.example.com", Sub: "sub-2"}).Identifier()
	if got, err := state.GetUserByOIDCIdentifier(identifier2); err != nil || strconv.FormatUint(uint64(got.ID), 10) != alice {
		t.Errorf("GetUserByOIDCIdentifier(sub-2) = %+v, %v, want alice", got, err)
	}
	if _, err := state.GetUserByOIDCIdentifier(identifier); err == nil {
		t.Error("sub-1 still resolves to a user after alice's identity was replaced")
	}

	// An identity owned by another user is left alone, and the caller
	// learns bob didn't get it.
	if err := svc.SetOIDCIdentity(t.Context(), "acme", bob, "sub-2"); !errors.Is(err, ErrIdentityTaken) {
		t.Errorf("SetOIDCIdentity of an identity owned by alice = %v, want ErrIdentityTaken", err)
	}
	bobUser, err := state.GetUserByID(types.UserID(mustParseUint(t, bob)))
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if bobUser.ProviderIdentifier.Valid {
		t.Errorf("bob got identity %q, want none", bobUser.ProviderIdentifier.String)
	}

	if err := svc.SetOIDCIdentity(t.Context(), "missing", alice, "sub-1"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("unknown org error = %v, want ErrNotRunning", err)
	}
}

func mustParseUint(t *testing.T, s string) uint {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return uint(n)
}

// TestCloseStopsStarts verifies that after Close no operation starts an
// organization's headscale again, so its database stays closed.
func TestCloseStopsStarts(t *testing.T) {
	cases := []struct {
		name  string
		start func(ctx context.Context, svc *Service) error
	}{
		{"Ensure", func(ctx context.Context, svc *Service) error {
			_, err := svc.Ensure(ctx, "acme")
			return err
		}},
		{"Wake", func(ctx context.Context, svc *Service) error {
			_, err := svc.Ensure(ctx, "acme")
			return err
		}},
		{"DialOrg", func(ctx context.Context, svc *Service) error {
			conn, err := svc.DialOrg(ctx, "acme")
			if err == nil {
				conn.Close()
			}
			return err
		}},
		{"restart", func(ctx context.Context, svc *Service) error {
			return svc.restartHeadscale(ctx, "acme")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newRealService(t)
			if _, err := svc.Create(t.Context(), "acme"); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Ensure(t.Context(), "acme"); err != nil {
				t.Fatal(err)
			}
			if err := svc.Close(t.Context()); err != nil {
				t.Fatalf("Close: %v", err)
			}

			if err := tc.start(t.Context(), svc); !errors.Is(err, ErrNotRunning) {
				t.Errorf("%s after Close: error = %v, want ErrNotRunning", tc.name, err)
			}
			if _, ok := svc.running("acme"); ok {
				t.Errorf("acme's headscale is running after Close")
			}
		})
	}
}

// TestOrgRestartsDoNotBlockEachOther verifies that restarting one
// organization's headscale doesn't hold up a different organization's
// restart.
func TestOrgRestartsDoNotBlockEachOther(t *testing.T) {
	svc := newTestService(t)
	createStarted(t, svc, "acme")
	createStarted(t, svc, "other")

	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	svc.createFn = func(p headscaleParams) (*hscontrol.Headscale, error) {
		if p.subdomain == "acme" {
			entered <- struct{}{}
			<-release
		}
		return nil, nil
	}
	go func() {
		defer close(done)
		if err := svc.restartHeadscale(t.Context(), "acme"); err != nil {
			t.Errorf("restartHeadscale(acme): %v", err)
		}
	}()
	<-entered
	t.Cleanup(func() {
		close(release)
		<-done
	})

	otherErr := make(chan error, 1)
	go func() { otherErr <- svc.restartHeadscale(t.Context(), "other") }()
	select {
	case err := <-otherErr:
		if err != nil {
			t.Errorf("restartHeadscale(other): %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("restartHeadscale(other) blocked on acme's in-flight restart")
	}
}
