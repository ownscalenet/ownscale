package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/hscl/hstest"
)

// sshTestOrg builds Handlers with a base domain, an organization, and an
// owner linked to headscale user "1" (alice), who owns the fake's online
// node "1".
func sshTestOrg(t *testing.T) (*Handlers, sqlcgen.Organization, sqlcgen.User, *hscl.Client) {
	t.Helper()
	h, queries := newTestHandlersWithBaseDomain(t, testBaseDomain)
	org := insertOrg(t, queries, "acme")
	client, _, _, _ := newFakeHeadscaleServer(t)
	h.hs = fakeHeadscaleClients{client: client}
	owner := callerWithRole(t, h, sqlcgen.User{OrganizationID: org.ID}, RoleOwner, "1")
	return h, org, owner, client
}

func TestCreateSSHSession(t *testing.T) {
	h, org, owner, client := sshTestOrg(t)

	req := withPathValue(withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/ssh-session", nil), owner), client, org), "id", "1")
	rec := httptest.NewRecorder()
	h.CreateSSHSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got sshSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := sshSessionResponse{
		ControlURL:  "https://acme." + testBaseDomain,
		AuthKey:     "nodekey:newkey",
		AuthKeyID:   "2",
		Hostname:    "alice-console-ssh",
		Target:      "100.64.0.1",
		MachineName: "node1",
		LoginName:   "alice",
	}
	if got != want {
		t.Errorf("session = %+v, want %+v", got, want)
	}
}

func TestCreateSSHSessionRejects(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		wantCode int
	}{
		{"unknown machine", "99", http.StatusNotFound},
		{"internal funnel machine", "2", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, org, owner, client := sshTestOrg(t)

			req := withPathValue(withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/"+tt.id+"/ssh-session", nil), owner), client, org), "id", tt.id)
			rec := httptest.NewRecorder()
			h.CreateSSHSession(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
		})
	}
}

// TestCreateSSHSessionUnreachableMachine covers the states a session can't
// be opened in, which the shared fake headscale has no nodes for.
func TestCreateSSHSessionUnreachableMachine(t *testing.T) {
	tests := []struct {
		name string
		node string
	}{
		{"offline", `{"id":"1","name":"n","givenName":"n","ipAddresses":["100.64.0.1"],"user":{"id":"1","name":"alice"},"online":false}`},
		{"expired", `{"id":"1","name":"n","givenName":"n","ipAddresses":["100.64.0.1"],"user":{"id":"1","name":"alice"},"online":true,"expiry":"2020-01-01T00:00:00Z"}`},
		{"no address", `{"id":"1","name":"n","givenName":"n","ipAddresses":[],"user":{"id":"1","name":"alice"},"online":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, org, owner, _ := sshTestOrg(t)
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/node/{id}", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"node":%s}`, tt.node)
			})
			client := hscl.NewUnix(hstest.Socket(t, mux))

			req := withPathValue(withHeadscaleClientOrg(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/ssh-session", nil), owner), client, org), "id", "1")
			rec := httptest.NewRecorder()
			h.CreateSSHSession(rec, req)

			if rec.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSSHTargetAddress(t *testing.T) {
	tests := []struct {
		name string
		ips  []string
		want string
	}{
		{"prefers IPv4", []string{"fd7a:115c::1", "100.64.0.1"}, "100.64.0.1"},
		{"falls back to IPv6", []string{"fd7a:115c::1"}, "fd7a:115c::1"},
		{"none", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sshTargetAddress(hscl.Node{IPAddresses: tt.ips}); got != tt.want {
				t.Errorf("sshTargetAddress(%v) = %q, want %q", tt.ips, got, tt.want)
			}
		})
	}
}

func TestBrowserNodeHostname(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"alice", "alice-console-ssh"},
		{"Alice Smith", "alicesmith-console-ssh"},
		{"alice.smith@example.com", "alice-smithexample-com-console-ssh"},
		{"!!!", "console-ssh"},
	}
	for _, tt := range tests {
		if got := browserNodeHostname(tt.in); got != tt.want {
			t.Errorf("browserNodeHostname(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSSHLoginName(t *testing.T) {
	tests := []struct {
		user hscl.User
		want string
	}{
		{hscl.User{Name: "alice", Email: "alice@example.com"}, "alice"},
		{hscl.User{Name: "alice", Email: ""}, "alice"},
		{hscl.User{Name: "Bob Jones"}, "bobjones"},
	}
	for _, tt := range tests {
		if got := sshLoginName(tt.user); got != tt.want {
			t.Errorf("sshLoginName(%+v) = %q, want %q", tt.user, got, tt.want)
		}
	}
}
