package orgs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"tailscale.com/tsnet"

	"github.com/ownscalenet/ownscale/hscl"
	"tailscale.com/tailcfg/nodecap"
)

// fakeProvider records the challenge records written to it.
type fakeProvider struct {
	mu      sync.Mutex
	records []libdns.RR
}

func (p *fakeProvider) AppendRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
			p := &scopedProvider{Provider: fake, domain: "netacme.os.example.com"}
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

// TestNodeCertChallenge connects a real node to an organization with node
// certificates on and checks it gets a certificate name and can publish its
// DNS-01 challenge through the provider.
func TestNodeCertChallenge(t *testing.T) {
	svc := newRealService(t)
	fake := &fakeProvider{}
	svc.certs = &nodeCerts{provider: fake, providerName: "rfc2136", zone: "example.com"}
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
