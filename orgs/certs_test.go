package orgs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"tailscale.com/tsnet"

	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"tailscale.com/tailcfg/nodecap"
)

type fakeProvider struct {
	mu      sync.Mutex
	records []libdns.RR
	fail    error
}

func (p *fakeProvider) AppendRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return nil, p.fail
	}
	for _, r := range recs {
		p.records = append(p.records, r.RR())
	}
	return recs, nil
}

func (p *fakeProvider) DeleteRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	return recs, nil
}

func (p *fakeProvider) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var names []string
	for _, r := range p.records {
		names = append(names, r.Name)
	}
	return names
}

func TestCreateHeadscaleCerts(t *testing.T) {
	certs := &nodeCerts{provider: &fakeProvider{}, providerName: "rfc2136", zone: "example.com"}
	tests := []struct {
		name     string
		certs    *nodeCerts
		magicDNS bool
		want     bool
	}{
		{"no provider", nil, true, false},
		{"provider", certs, true, true},
		{"provider without MagicDNS", certs, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := testHeadscaleParams(t, tt.certs)
			p.hsCfg.DNS.MagicDNS = tt.magicDNS
			app, err := createHeadscale(p)
			if err != nil {
				t.Fatalf("createHeadscale: %v", err)
			}
			t.Cleanup(func() { _ = app.GetState().Close() })

			cfg := app.GetState().DebugConfig()
			if got := cfg.DNSConfig.Certificates.Enabled; got != tt.want {
				t.Errorf("Certificates.Enabled = %v, want %v", got, tt.want)
			}
			if got := cfg.DNSChallenge.Provider != nil; got != tt.want {
				t.Errorf("DNSChallenge.Provider set = %v, want %v", got, tt.want)
			}
			if tt.want && cfg.DNSConfig.Certificates.Zone != "example.com" {
				t.Errorf("Certificates.Zone = %q, want example.com", cfg.DNSConfig.Certificates.Zone)
			}
		})
	}
}

func newScopedProvider(t *testing.T, fake *fakeProvider, perOrgPerWeek int) (*scopedProvider, *sql.DB) {
	t.Helper()
	pool, queries := dbtest.New(t)
	org := dbtest.CreateOrg(t, queries.Queries, "acme")
	certs := &nodeCerts{provider: fake, queries: queries, perOrgPerWeek: perOrgPerWeek}
	return &scopedProvider{certs: certs, orgID: org.ID, domain: "netacme.os.example.com"}, pool
}

func TestScopedProvider(t *testing.T) {
	tests := []struct {
		name    string
		rec     libdns.Record
		wantErr bool
	}{
		{"own node challenge", libdns.TXT{Name: "_acme-challenge.node.netacme.os"}, false},
		{"other org", libdns.TXT{Name: "_acme-challenge.node.netother.os"}, true},
		{"suffix lookalike", libdns.TXT{Name: "_acme-challenge.node.xnetacme.os"}, true},
		{"not a challenge", libdns.TXT{Name: "node.netacme.os"}, true},
		{"not TXT", libdns.Address{Name: "_acme-challenge.node.netacme.os"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeProvider{}
			p, _ := newScopedProvider(t, fake, 10)
			_, err := p.AppendRecords(t.Context(), "example.com.", []libdns.Record{tt.rec})
			if (err != nil) != tt.wantErr {
				t.Fatalf("AppendRecords error = %v, wantErr %v", err, tt.wantErr)
			}
			if _, err := p.DeleteRecords(t.Context(), "example.com.", []libdns.Record{tt.rec}); (err != nil) != tt.wantErr {
				t.Fatalf("DeleteRecords error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := len(fake.names()); got != 0 == tt.wantErr {
				t.Errorf("provider got %d records, wantErr %v", got, tt.wantErr)
			}
		})
	}
}

func TestScopedProviderLimit(t *testing.T) {
	challenge := func(node string) libdns.Record {
		return libdns.TXT{Name: "_acme-challenge." + node + ".netacme.os", Text: strings.Repeat("a", 43)}
	}
	tests := []struct {
		name          string
		perOrgPerWeek int
		// before are challenges published, and aged a week, before nodes.
		before    []string
		nodes     []string
		wantErr   []bool
		wantCount int64
	}{
		{"under limit", 2, nil, []string{"a", "b"}, []bool{false, false}, 2},
		{"over limit", 2, nil, []string{"a", "b", "c"}, []bool{false, false, true}, 2},
		{"renewal doesn't count", 2, nil, []string{"a", "a", "b", "a"}, []bool{false, false, false, false}, 2},
		{"case folded", 1, nil, []string{"a", "A"}, []bool{false, false}, 1},
		{"no limit", 0, nil, []string{"a", "b", "c"}, []bool{false, false, false}, 3},
		{"older than a week", 1, []string{"a", "b"}, []string{"a", "c", "d"}, []bool{false, false, true}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, pool := newScopedProvider(t, &fakeProvider{}, tt.perOrgPerWeek)
			queries := p.certs.queries
			for _, node := range tt.before {
				if err := queries.CreateCertName(t.Context(), sqlcgen.CreateCertNameParams{OrganizationID: p.orgID, Name: node + ".netacme.os.example.com"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.ExecContext(t.Context(), "UPDATE cert_names SET first_seen_at = datetime('now', '-8 days')"); err != nil {
				t.Fatal(err)
			}
			for i, node := range tt.nodes {
				_, err := p.AppendRecords(t.Context(), "example.com.", []libdns.Record{challenge(node)})
				if (err != nil) != tt.wantErr[i] || (err != nil && !errors.Is(err, errCertLimit)) {
					t.Errorf("AppendRecords(%s) error = %v, wantErr %v", node, err, tt.wantErr[i])
				}
			}
			got, err := queries.CountCertNamesThisWeek(t.Context(), p.orgID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantCount {
				t.Errorf("CountCertNamesThisWeek = %d, want %d", got, tt.wantCount)
			}
		})
	}
}

func TestScopedProviderFailureDoesntCount(t *testing.T) {
	fake := &fakeProvider{fail: errors.New("provider down")}
	p, _ := newScopedProvider(t, fake, 1)
	rec := libdns.TXT{Name: "_acme-challenge.a.netacme.os", Text: strings.Repeat("a", 43)}
	if _, err := p.AppendRecords(t.Context(), "example.com.", []libdns.Record{rec}); err == nil {
		t.Fatal("AppendRecords succeeded with a failing provider")
	}
	n, err := p.certs.queries.CountCertNamesThisWeek(t.Context(), p.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("CountCertNamesThisWeek = %d after a failed append, want 0", n)
	}
}

// TestNodeCertChallenge connects a real node to an organization with node
// certificates on and checks it gets a certificate name and can publish its
// DNS-01 challenge through the provider.
func TestNodeCertChallenge(t *testing.T) {
	svc := newRealService(t)
	fake := &fakeProvider{}
	svc.certs = &nodeCerts{provider: fake, providerName: "rfc2136", zone: "example.com", queries: svc.queries}
	org := createStarted(t, svc, "acme")
	client, err := svc.Client("acme")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	authKey := newAuthKey(t, client)
	port, _ := svc.LookupPort("acme")

	srv := &tsnet.Server{
		Dir:        t.TempDir(),
		ControlURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		AuthKey:    authKey,
		Hostname:   "web",
		Ephemeral:  true,
		Logf:       func(string, ...any) {},
		UserLogf:   func(string, ...any) {},
	}
	t.Cleanup(func() { _ = srv.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := srv.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	lc, err := srv.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	st, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "web." + svc.tailnetDomain(org.TailnetLabel)
	if len(st.CertDomains) != 1 || st.CertDomains[0] != want {
		t.Fatalf("CertDomains = %v, want [%s]", st.CertDomains, want)
	}

	if !st.Self.HasCap(nodecap.HTTPS) {
		t.Error("node lacks the HTTPS capability tailscale serve needs")
	}

	value := strings.Repeat("a", 43)
	if err := lc.SetDNS(ctx, "_acme-challenge."+want, value); err != nil {
		t.Fatalf("SetDNS: %v", err)
	}
	wantRel := "_acme-challenge.web." + org.TailnetLabel + ".os"
	if got := fake.names(); len(got) != 1 || got[0] != wantRel {
		t.Errorf("provider records = %v, want [%s]", got, wantRel)
	}

	err = lc.SetDNS(ctx, "_acme-challenge.other."+svc.tailnetDomain(org.TailnetLabel), value)
	if err == nil {
		t.Error("SetDNS for another node's name succeeded, want error")
	}
}

func newAuthKey(t *testing.T, client *hscl.Client) string {
	t.Helper()
	user, err := client.CreateUser(t.Context(), hscl.CreateUserParams{Name: "alice", Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, err := client.CreatePreAuthKey(t.Context(), hscl.CreatePreAuthKeyParams{UserID: user.ID, Ephemeral: true, Expiration: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreatePreAuthKey: %v", err)
	}
	if key.Key == "" {
		t.Fatal(errors.New("empty pre-auth key"))
	}
	return key.Key
}
