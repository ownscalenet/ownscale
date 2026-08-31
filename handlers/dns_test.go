package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ownscalenet/ownscale/dockerctl"
)

func TestGetDNSSettingsReflectsProvisionedDefaults(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/dns", nil), member), member, org)
	rec := httptest.NewRecorder()
	h.GetDNSSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		DNS       dockerctl.DNSConfig `json:"dns"`
		CanManage bool                `json:"canManage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.DNS.MagicDNS {
		t.Error("MagicDNS = false, want true (headscale's default)")
	}
	if resp.DNS.BaseDomain != "acme.ownscale" {
		t.Errorf("BaseDomain = %q, want %q", resp.DNS.BaseDomain, "acme.ownscale")
	}
	if resp.CanManage {
		t.Error("canManage = true for a plain member, want false")
	}
}

func TestUpdateDNSSettingsRequiresManager(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	body := `{"baseDomain":"acme.ownscale","magicDns":true}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns", strings.NewReader(body)), member), member, org)
	rec := httptest.NewRecorder()
	h.UpdateDNSSettings(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateDNSSettingsRejectsInvalidInput(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"baseDomain":"acme.ownscale","nameservers":["not-an-ip"]}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns", strings.NewReader(body)), owner), owner, org)
	rec := httptest.NewRecorder()
	h.UpdateDNSSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateDNSSettingsPersistsAndReturnsOK(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	body := `{"baseDomain":"acme.ownscale","magicDns":true,"nameservers":{"global":["1.1.1.1","8.8.8.8"]},` +
		`"searchDomains":["internal.acme.example.com"],"extraRecords":[{"name":"server.acme.ownscale","type":"A","value":"10.0.0.1"}]}`
	req := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodPut, "/api/dns", strings.NewReader(body)), owner), owner, org)
	rec := httptest.NewRecorder()
	h.UpdateDNSSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	getReq := withActiveOrg(withTestUser(httptest.NewRequest(http.MethodGet, "/api/dns", nil), owner), owner, org)
	getRec := httptest.NewRecorder()
	h.GetDNSSettings(getRec, getReq)

	var resp struct {
		DNS       dockerctl.DNSConfig `json:"dns"`
		CanManage bool                `json:"canManage"`
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
	if !resp.CanManage {
		t.Error("canManage = false for an owner, want true")
	}
}
