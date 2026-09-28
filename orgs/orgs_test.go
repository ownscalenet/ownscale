package orgs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/juanfont/headscale/hscontrol"
	"github.com/juanfont/headscale/hscontrol/types"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// newRealService builds a Service backed by a fresh database that starts
// real headscale instances.
func newRealService(t *testing.T) *Service {
	t.Helper()
	_, queries := dbtest.New(t)
	return NewService(queries, "example.com", "https://auth.example.com", config.DerpConfig{}, slog.New(slog.DiscardHandler), t.TempDir())
}

// newTestService is newRealService, but never starts headscale.
func newTestService(t *testing.T) *Service {
	t.Helper()
	svc := newRealService(t)
	svc.createFn = noopCreateFn
	return svc
}

func noopCreateFn(string, string, string, int, *HeadscaleConfig, config.DerpConfig) (*hscontrol.Headscale, error) {
	return nil, nil
}

// TestCreate verifies that Create stores an active organization with its
// OIDC client and a HeadscaleConfig holding the client's credentials.
func TestCreate(t *testing.T) {
	svc := newTestService(t)

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if org.Status != StatusActive || org.Subdomain != "acme" {
		t.Errorf("org = %+v, want active acme", org)
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

func TestCreateRejectsInvalidSubdomain(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.Create(t.Context(), "INVALID!"); err == nil {
		t.Fatal("Create with invalid subdomain: want error, got nil")
	}
}

func TestCreateSurfacesHeadscaleFailure(t *testing.T) {
	svc := newTestService(t)
	svc.createFn = func(string, string, string, int, *HeadscaleConfig, config.DerpConfig) (*hscontrol.Headscale, error) {
		return nil, errors.New("headscale failed (injected)")
	}

	if _, err := svc.Create(t.Context(), "acme"); err == nil {
		t.Fatal("Create: want error when createFn fails, got nil")
	}
	if _, err := svc.queries.GetOrganizationBySubdomain(t.Context(), "acme"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("organization row after failed Create: err = %v, want sql.ErrNoRows", err)
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
	svc.createFn = func(string, string, string, int, *HeadscaleConfig, config.DerpConfig) (*hscontrol.Headscale, error) {
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

func TestNextSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		taken []string
		want  string // "" means a random subdomain
	}{
		{"empty database", nil, "aaaa"},
		{"skips taken fixed subdomains", []string{"aaaa", "bbbb"}, "cccc"},
		{"falls back to random", fixedSubdomains, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			for _, sub := range tt.taken {
				if _, err := svc.Create(t.Context(), sub); err != nil {
					t.Fatalf("Create(%q): %v", sub, err)
				}
			}
			got, err := svc.NextSubdomain(t.Context())
			if err != nil {
				t.Fatalf("NextSubdomain: %v", err)
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("NextSubdomain = %q, want %q", got, tt.want)
			}
			if tt.want == "" {
				if slices.Contains(fixedSubdomains, got) {
					t.Errorf("NextSubdomain = %q, want a random subdomain", got)
				}
				if err := ValidateSubdomain(got); err != nil {
					t.Errorf("random subdomain %q is invalid: %v", got, err)
				}
			}
		})
	}
}

// TestStartRecoversOrganizations checks that Start restarts the headscale
// instance of an organization left over from a previous process.
func TestStartRecoversOrganizations(t *testing.T) {
	seed := newTestService(t)
	if _, err := seed.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A second Service on the same database simulates a process restart.
	svc := NewService(seed.queries, "example.com", "https://auth.example.com", config.DerpConfig{}, slog.New(slog.DiscardHandler), t.TempDir())
	svc.createFn = noopCreateFn

	if err := svc.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := svc.LookupPort("acme"); !ok {
		t.Error("port not registered for recovered org \"acme\"")
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
	svc.orgs.Store("acme", &orgRuntime{port: port})
	svc.orgs.Store("other", &orgRuntime{port: port})

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

	if err := svc.startHeadscale(t.Context(), "acme", newHeadscaleConfig("acme", OIDCConfig{Issuer: "https://auth.example.com"})); err != nil {
		t.Fatalf("startHeadscale: %v", err)
	}
	inst, ok := svc.running("acme")
	if !ok {
		t.Fatal("acme not running after startHeadscale")
	}
	t.Cleanup(func() { inst.cancel(); <-inst.exited })

	if port, _ := svc.LookupPort("acme"); port == taken {
		t.Errorf("port = %d, want a port other than the taken one", port)
	}
	if _, err := inst.client.ListUsers(t.Context()); err != nil {
		t.Errorf("ListUsers: %v", err)
	}
}

func TestCreateHeadscaleDERP(t *testing.T) {
	custom := config.DerpConfig{
		Hostname: "derp.example.com", IPv4: "203.0.113.5", STUNPort: 3478,
		RegionID: 900, RegionCode: "custom", RegionName: "Custom",
	}
	tests := []struct {
		name       string
		derp       config.DerpConfig
		wantRegion bool
	}{
		{"no custom relay", config.DerpConfig{}, false},
		{"custom relay", custom, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hsCfg := newHeadscaleConfig("acme", OIDCConfig{Issuer: "https://auth.example.com"})
			app, err := createHeadscale(t.TempDir(), "acme", "example.com", 0, hsCfg, tt.derp)
			if err != nil {
				t.Fatalf("createHeadscale: %v", err)
			}
			t.Cleanup(func() { _ = app.GetState().Close() })

			dm := app.GetState().DebugConfig().DERP.DERPMap
			if !tt.wantRegion {
				if dm != nil {
					t.Fatalf("DERPMap = %+v, want nil", dm)
				}
				return
			}
			region := dm.Regions[900]
			if region == nil || region.RegionCode != "custom" || len(region.Nodes) != 1 {
				t.Fatalf("region 900 = %+v", region)
			}
			if n := region.Nodes[0]; n.Name != "customa" || n.HostName != "derp.example.com" || n.IPv4 != "203.0.113.5" || n.STUNPort != 3478 || n.DERPPort != 0 {
				t.Errorf("node = %+v", n)
			}
		})
	}
}

func TestSetOIDCIdentity(t *testing.T) {
	svc := newRealService(t)
	if _, err := svc.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	inst, _ := svc.running("acme")
	t.Cleanup(func() { inst.cancel(); <-inst.exited })
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

	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-1"); err != nil {
		t.Fatalf("SetOIDCIdentity: %v", err)
	}
	got, err := state.GetUserByOIDCIdentifier(identifier)
	if err != nil {
		t.Fatalf("GetUserByOIDCIdentifier: %v", err)
	}
	if strconv.FormatUint(uint64(got.ID), 10) != alice || got.Name != "alice" || got.Email != "alice@example.com" || got.Provider != "oidc" {
		t.Errorf("user = %+v, want alice unchanged apart from the OIDC identity", got)
	}

	// A user that already has an identity, and an identity owned by
	// another user, are both left alone.
	if err := svc.SetOIDCIdentity(t.Context(), "acme", alice, "sub-2"); err != nil {
		t.Fatalf("SetOIDCIdentity again: %v", err)
	}
	if err := svc.SetOIDCIdentity(t.Context(), "acme", bob, "sub-1"); err != nil {
		t.Fatalf("SetOIDCIdentity taken identity: %v", err)
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
