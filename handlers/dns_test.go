package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/orgs"
	"tailscale.com/tailcfg"
)

func TestGetDNSSettingsReflectsProvisionedDefaults(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/dns", nil), member)
	rec := httptest.NewRecorder()
	h.GetDNSSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		DNS               orgs.DNSConfig `json:"dns"`
		CanManage         bool           `json:"canManage"`
		HTTPSCertificates bool           `json:"httpsCertificates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.DNS.MagicDNS {
		t.Error("MagicDNS = false, want true (headscale's default)")
	}
	if want := "netacme.example.com"; resp.DNS.BaseDomain != want {
		t.Errorf("BaseDomain = %q, want %q", resp.DNS.BaseDomain, want)
	}
	if resp.CanManage {
		t.Error("canManage = true for a plain member, want false")
	}
	if resp.HTTPSCertificates {
		t.Error("httpsCertificates = true without a DNS provider, want false")
	}
	// The frontend iterates over split, so it must be {} rather than null.
	if !strings.Contains(rec.Body.String(), `"split":{}`) {
		t.Errorf("body = %s, want an empty split object", rec.Body.String())
	}
}

func TestUpdateDNSSettingsRequiresManager(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"magicDns":true}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns", strings.NewReader(body)), member)
	rec := httptest.NewRecorder()
	h.UpdateDNSSettings(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateDNSSettingsRejectsInvalidInput(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"nameservers":["not-an-ip"]}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns", strings.NewReader(body)), owner)
	rec := httptest.NewRecorder()
	h.UpdateDNSSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateDNSSettingsPersistsAndReturnsOK(t *testing.T) {
	h, _, users := newOrgTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"magicDns":true,` +
		`"nameservers":{"global":["1.1.1.1","8.8.8.8"],"split":{"Corp.Example.com.":["10.0.0.53"]}},` +
		`"searchDomains":["internal.acme.example.com"],"extraRecords":[{"name":"server.acme.ownscale","type":"A","value":"10.0.0.1"}]}`
	req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns", strings.NewReader(body)), owner)
	rec := httptest.NewRecorder()
	h.UpdateDNSSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	getReq := withTestUser(httptest.NewRequest(http.MethodGet, "/api/dns", nil), owner)
	getRec := httptest.NewRecorder()
	h.GetDNSSettings(getRec, getReq)

	var resp struct {
		DNS       orgs.DNSConfig `json:"dns"`
		CanManage bool           `json:"canManage"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.DNS.Nameservers.Global) != 2 || resp.DNS.Nameservers.Global[1] != "8.8.8.8" {
		t.Errorf("Nameservers = %v, want [1.1.1.1 8.8.8.8]", resp.DNS.Nameservers.Global)
	}
	if len(resp.DNS.ExtraRecords) != 1 || resp.DNS.ExtraRecords[0].Value != "10.0.0.1" {
		t.Errorf("ExtraRecords = %v, want one record with value 10.0.0.1", resp.DNS.ExtraRecords)
	}
	if got := resp.DNS.Nameservers.Split["corp.example.com"]; !slices.Equal(got, []string{"10.0.0.53"}) {
		t.Errorf("Split = %v, want corp.example.com -> [10.0.0.53]", resp.DNS.Nameservers.Split)
	}
	if !resp.CanManage {
		t.Error("canManage = false for an owner, want true")
	}
}

func TestUpdateDERPSettings(t *testing.T) {
	defaults := orgs.DERPSettings{Tailscale: true, Servers: []orgs.DERPServer{}}
	tests := []struct {
		name       string
		role       string
		body       string
		wantStatus int
		want       orgs.DERPSettings
	}{
		{"member", RoleMember, `{"ownscale":true}`, http.StatusForbidden, defaults},
		{"nothing chosen", RoleOwner, `{"tailscale":false}`, http.StatusBadRequest, defaults},
		{"invalid IPv4", RoleAdmin, `{"servers":[{"hostName":"derp.example.com","ipv4":"nope"}]}`, http.StatusBadRequest, defaults},
		{
			"admin", RoleAdmin,
			`{"ownscale":true,"servers":[{"hostName":"derp.example.com","ipv6":"2001:db8::1"}]}`,
			http.StatusOK,
			orgs.DERPSettings{Ownscale: true, Servers: []orgs.DERPServer{{HostName: "derp.example.com", IPv6: "2001:db8::1"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, users := newOrgTestHandlers(t, tt.role)
			caller := users[0]

			req := withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns/derp", strings.NewReader(tt.body)), caller)
			rec := httptest.NewRecorder()
			h.UpdateDERPSettings(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			req = withTestUser(httptest.NewRequest(http.MethodGet, "/api/dns", nil), caller)
			rec = httptest.NewRecorder()
			h.GetDNSSettings(rec, req)
			var resp struct {
				DERP orgs.DERPSettings `json:"derp"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v, body=%s", err, rec.Body.String())
			}
			if !reflect.DeepEqual(resp.DERP, tt.want) {
				t.Errorf("derp = %+v, want %+v", resp.DERP, tt.want)
			}
		})
	}
}

func TestServeBuiltinDERPMap(t *testing.T) {
	h, _ := newTestHandlers(t)
	rec := httptest.NewRecorder()
	h.ServeBuiltinDERPMap(rec, httptest.NewRequest(http.MethodGet, orgs.BuiltinDERPMapPath, nil))

	var dm tailcfg.DERPMap
	if err := json.Unmarshal(rec.Body.Bytes(), &dm); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, rec.Body.String())
	}
	if !reflect.DeepEqual(&dm, orgs.BuiltinDERPMap()) {
		t.Errorf("served map = %s, want orgs.BuiltinDERPMap()", rec.Body.String())
	}
}
