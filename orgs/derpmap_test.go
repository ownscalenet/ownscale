package orgs

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
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
			derpCfg := config.DerpConfig{MapURL: derpMapServer(t, tt.status, mustJSON(t, testDERPMap(2)))}
			cfg := &config.Config{
				BaseDomain:      "example.com",
				DataDir:         t.TempDir(),
				Derp:            derpCfg,
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

	svc.cfg.Derp.MapURL = derpMapServer(t, http.StatusOK, mustJSON(t, testDERPMap(5)))
	if err := svc.refreshDERPMap(t.Context()); err != nil {
		t.Fatalf("refreshDERPMap: %v", err)
	}
	if !hasRegion(5) || hasRegion(1) {
		t.Fatalf("DERP map after refresh has %d regions, want only region 5", inst.app.GetState().DERPMap().Regions().Len())
	}

	svc.cfg.Derp.MapURL = derpMapServer(t, http.StatusBadGateway, "")
	if err := svc.refreshDERPMap(t.Context()); err == nil {
		t.Fatal("refreshDERPMap succeeded on a failing endpoint")
	}
	if !hasRegion(5) {
		t.Errorf("DERP map after failed refresh has %d regions, want region 5 kept", inst.app.GetState().DERPMap().Regions().Len())
	}
}
