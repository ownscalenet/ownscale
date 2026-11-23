package orgs

import (
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"tailscale.com/tailcfg"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db/dbtest"
)

// derpMapServer serves body with status at the returned URL.
func derpMapServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetchDERPMap(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantRegions []tailcfg.DERPRegionID
		wantErr     bool
	}{
		{"ok", http.StatusOK, mustJSON(t, testDERPMap(3)), []tailcfg.DERPRegionID{3}, false},
		{"non-200", http.StatusInternalServerError, mustJSON(t, testDERPMap(3)), nil, true},
		{"invalid JSON", http.StatusOK, "{", nil, true},
		{"no regions", http.StatusOK, `{"Regions":{}}`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := derpMapServer(t, tt.status, tt.body)
			dm, err := fetchDERPMap(t.Context(), http.DefaultClient, url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("fetchDERPMap() err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got := slices.Sorted(maps.Keys(dm.Regions)); !slices.Equal(got, tt.wantRegions) {
				t.Errorf("regions = %v, want %v", got, tt.wantRegions)
			}
		})
	}
}

func TestCurrentDERPMap(t *testing.T) {
	tests := []struct {
		name       string
		stored     *tailcfg.DERPMap
		status     int
		wantRegion tailcfg.DERPRegionID
		wantErr    bool
	}{
		{"stored map", testDERPMap(1), http.StatusInternalServerError, 1, false},
		{"fetches when none stored", nil, http.StatusOK, 2, false},
		{"fetch fails", nil, http.StatusInternalServerError, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, queries := dbtest.New(t)
			cfg := &config.Config{
				FastOrgs:        true,
				BaseDomain:      "example.com",
				DataDir:         t.TempDir(),
				DERPMapURL:      derpMapServer(t, tt.status, mustJSON(t, testDERPMap(2))),
				FixedSubdomains: testFixedSubdomains,
				ContentDomain:   "example.com",
			}
			svc, err := NewService(queries, cfg, "https://auth.example.com", slog.New(slog.DiscardHandler), nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.stored != nil {
				svc.derpMap.Store(tt.stored)
			}
			dm, err := svc.currentDERPMap(t.Context())
			if (err != nil) != tt.wantErr {
				t.Fatalf("currentDERPMap() err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if dm.Regions[tt.wantRegion] == nil {
				t.Errorf("regions = %v, want %d", slices.Sorted(maps.Keys(dm.Regions)), tt.wantRegion)
			}
			if svc.derpMap.Load() != dm {
				t.Error("fetched map was not stored")
			}
		})
	}
}

func TestRefreshDERPMapPushesToRunningOrgs(t *testing.T) {
	svc := newRealService(t)
	createStarted(t, svc, "acme")
	inst, ok := svc.running("acme")
	if !ok {
		t.Fatal("acme not running")
	}
	hasRegion := func(id tailcfg.DERPRegionID) bool {
		return inst.app.GetState().DERPMap().Regions().Contains(id)
	}

	svc.cfg.DERPMapURL = derpMapServer(t, http.StatusOK, mustJSON(t, testDERPMap(5)))
	if err := svc.refreshDERPMap(t.Context()); err != nil {
		t.Fatalf("refreshDERPMap: %v", err)
	}
	if !hasRegion(5) || hasRegion(1) {
		t.Fatalf("DERP map after refresh has %d regions, want only region 5", inst.app.GetState().DERPMap().Regions().Len())
	}

	svc.cfg.DERPMapURL = derpMapServer(t, http.StatusBadGateway, "")
	if err := svc.refreshDERPMap(t.Context()); err == nil {
		t.Fatal("refreshDERPMap succeeded on a failing endpoint")
	}
	if !hasRegion(5) {
		t.Errorf("DERP map after failed refresh has %d regions, want region 5 kept", inst.app.GetState().DERPMap().Regions().Len())
	}
}

// derpRegions returns the DERP regions subdomain's running headscale serves.
func derpRegions(t *testing.T, svc *Service, subdomain string) []tailcfg.DERPRegionID {
	t.Helper()
	inst, ok := svc.running(subdomain)
	if !ok {
		t.Fatalf("%s isn't running", subdomain)
	}
	var ids []tailcfg.DERPRegionID
	for id := range inst.app.GetState().DERPMap().Regions().All() {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func TestBuiltinDERPMap(t *testing.T) {
	node := BuiltinDERPMap().Regions[900].Nodes[0]
	if node.HostName != "derp1eu.ownscale.net" || node.RegionID != 900 {
		t.Errorf("node = %+v, want derp1eu.ownscale.net in region 900", node)
	}
}

func TestNormalizeDERPSettings(t *testing.T) {
	tests := []struct {
		name    string
		in      DERPSettings
		want    []DERPServer
		wantErr string
	}{
		{"Tailscale only", DERPSettings{Tailscale: true}, []DERPServer{}, ""},
		{
			"servers are trimmed and lowercased",
			DERPSettings{Servers: []DERPServer{
				{HostName: " DERP.Example.com. ", IPv4: " 203.0.113.1 ", IPv6: "2001:db8::1"},
				{HostName: "derp2.example.com"},
			}},
			[]DERPServer{
				{HostName: "derp.example.com", IPv4: "203.0.113.1", IPv6: "2001:db8::1"},
				{HostName: "derp2.example.com"},
			},
			"",
		},
		{"nothing chosen", DERPSettings{}, nil, "at least one"},
		{"empty hostname", DERPSettings{Servers: []DERPServer{{HostName: " "}}}, nil, "invalid DERP server hostname"},
		{"URL as hostname", DERPSettings{Servers: []DERPServer{{HostName: "https://derp.example.com"}}}, nil, "invalid DERP server hostname"},
		{"duplicate", DERPSettings{Servers: []DERPServer{{HostName: "derp.example.com"}, {HostName: "DERP.example.com"}}}, nil, "duplicate"},
		{"IPv6 as IPv4", DERPSettings{Servers: []DERPServer{{HostName: "derp.example.com", IPv4: "2001:db8::1"}}}, nil, "invalid IPv4"},
		{"IPv4 as IPv6", DERPSettings{Servers: []DERPServer{{HostName: "derp.example.com", IPv6: "203.0.113.1"}}}, nil, "invalid IPv6"},
		{"IPv4-mapped IPv6", DERPSettings{Servers: []DERPServer{{HostName: "derp.example.com", IPv6: "::ffff:203.0.113.1"}}}, nil, "invalid IPv6"},
		{"too many", DERPSettings{Servers: make([]DERPServer, maxDERPServers+1)}, nil, "at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDERPSettings(tt.in)
			if tt.wantErr != "" {
				if _, ok := errors.AsType[*InvalidConfigError](err); !ok || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want an InvalidConfigError containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Servers, tt.want) {
				t.Errorf("servers = %+v, want %+v", got.Servers, tt.want)
			}
		})
	}
}

func TestSetDERPSettings(t *testing.T) {
	own := []DERPServer{{HostName: "derp.example.com", IPv4: "203.0.113.1"}, {HostName: "derp2.example.com"}}
	tests := []struct {
		name        string
		set         DERPSettings
		wantErr     bool
		wantRegions []tailcfg.DERPRegionID
	}{
		{"Tailscale only", DERPSettings{Tailscale: true}, false, []tailcfg.DERPRegionID{1}},
		{"Ownscale only", DERPSettings{Ownscale: true}, false, []tailcfg.DERPRegionID{900}},
		{"own servers only", DERPSettings{Servers: own}, false, []tailcfg.DERPRegionID{901, 902}},
		{"all", DERPSettings{Tailscale: true, Ownscale: true, Servers: own}, false, []tailcfg.DERPRegionID{1, 900, 901, 902}},
		{"invalid keeps the previous settings", DERPSettings{}, true, []tailcfg.DERPRegionID{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newRealService(t)
			org := createStarted(t, svc, "acme")
			o := svc.ForOrg(org)
			before, err := o.GetDERPSettings(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			err = o.SetDERPSettings(t.Context(), tt.set)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetDERPSettings err = %v, wantErr %v", err, tt.wantErr)
			}
			want := cmpDERPSettings(tt.set)
			if tt.wantErr {
				want = before
			}
			got, err := o.GetDERPSettings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("GetDERPSettings = %+v, want %+v", got, want)
			}
			if regions := derpRegions(t, svc, "acme"); !slices.Equal(regions, tt.wantRegions) {
				t.Errorf("regions = %v, want %v", regions, tt.wantRegions)
			}

			// A restart builds the same map from the stored settings.
			if err := svc.restartHeadscale(t.Context(), "acme"); err != nil {
				t.Fatal(err)
			}
			if regions := derpRegions(t, svc, "acme"); !slices.Equal(regions, tt.wantRegions) {
				t.Errorf("regions after restart = %v, want %v", regions, tt.wantRegions)
			}
		})
	}
}

// cmpDERPSettings returns d as GetDERPSettings returns it.
func cmpDERPSettings(d DERPSettings) DERPSettings {
	if d.Servers == nil {
		d.Servers = []DERPServer{}
	}
	return d
}

func TestDefaultDERPSettings(t *testing.T) {
	svc := newRealService(t)
	org := createStarted(t, svc, "acme")
	got, err := svc.ForOrg(org).GetDERPSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := (DERPSettings{Tailscale: true, Servers: []DERPServer{}}); !reflect.DeepEqual(got, want) {
		t.Errorf("GetDERPSettings = %+v, want %+v", got, want)
	}
}

func TestRefreshDERPMapKeepsEachOrgsSettings(t *testing.T) {
	svc := newRealService(t)
	createStarted(t, svc, "acme")
	globex := createStarted(t, svc, "globex")
	if err := svc.ForOrg(globex).SetDERPSettings(t.Context(), DERPSettings{Ownscale: true}); err != nil {
		t.Fatal(err)
	}

	svc.cfg.DERPMapURL = derpMapServer(t, http.StatusOK, mustJSON(t, testDERPMap(5)))
	if err := svc.refreshDERPMap(t.Context()); err != nil {
		t.Fatalf("refreshDERPMap: %v", err)
	}
	if regions := derpRegions(t, svc, "acme"); !slices.Equal(regions, []tailcfg.DERPRegionID{5}) {
		t.Errorf("acme regions = %v, want [5]", regions)
	}
	if regions := derpRegions(t, svc, "globex"); !slices.Equal(regions, []tailcfg.DERPRegionID{900}) {
		t.Errorf("globex regions = %v, want [900]", regions)
	}
}
