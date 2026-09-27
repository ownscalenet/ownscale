package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/rs/xid"
	"github.com/tailscale/hujson"

	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/funnelproxy"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/headscaleclient/hstest"
	"github.com/ownscalenet/ownscale/orgs"
)

// fakeHeadscaleClients stands in for orgs.Service's running headscale
// instances, returning a client for a fake headscale API.
type fakeHeadscaleClients struct {
	client *headscaleclient.Client
	err    error
}

func (f fakeHeadscaleClients) Client(string) (*headscaleclient.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

// newFakeHeadscaleServer builds a fake implementing enough of
// headscale's management API to exercise the /api/machines handlers, and
// a *headscaleclient.Client pointed at it. The returned *string is the
// in-memory ACL policy GetPolicy/SetPolicy serve/mutate, starting out with
// no "tagOwners" section (matching a freshly provisioned org).
func newFakeHeadscaleServer(t *testing.T) (*headscaleclient.Client, *[]*http.Request, *string, *[]string) {
	t.Helper()
	var requests []*http.Request
	policy := `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`
	var lastTags []string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"nodes":[
			{"id":"1","name":"node1","givenName":"node1","ipAddresses":["100.64.0.1"],
			 "user":{"id":"1","name":"alice"},"online":true,"tags":["tag:server"],
			 "approvedRoutes":["10.0.0.0/24"],"availableRoutes":["10.0.0.0/24"],
			 "createdAt":"2024-01-01T00:00:00Z"},
			{"id":"2","name":"funnel-acme","givenName":"funnel-acme","ipAddresses":["100.64.0.2"],
			 "user":{"id":"2","name":%q},"online":false,
			 "expiry":"2020-01-01T00:00:00Z","createdAt":"2024-01-01T00:00:00Z"}
		]}`, funnelproxy.FunnelHeadscaleUserName)
	})
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"user":{"id":"3","name":"new-user"}}`)
			return
		}
		fmt.Fprint(w, `{"users":[{"id":"1","name":"alice","email":"alice@example.com"},{"id":"2","name":"bob","email":"bob@example.com"}]}`)
	})
	mux.HandleFunc("/api/v1/user/1/rename/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/user/1", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/user/3", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/node/1/rename/", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/tags", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Tags []string `json:"tags"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastTags = body.Tags
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/approve_routes", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1/expire", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"node":{"id":"1"}}`)
	})
	mux.HandleFunc("/api/v1/node/1", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			var body struct {
				Policy string `json:"policy"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			policy = body.Policy
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprintf(w, `{"policy":%q}`, policy)
	})
	mux.HandleFunc("/api/v1/preauthkey", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"preAuthKey":{
				"id":"2","key":"nodekey:newkey","user":{"id":"1","name":"alice"},
				"reusable":true,"ephemeral":false,"used":false,"aclTags":["tag:server"],
				"createdAt":"2024-03-01T00:00:00Z","expiration":"2024-06-01T00:00:00Z"
			}}`)
			return
		}
		fmt.Fprint(w, `{"preAuthKeys":[
			{"id":"1","key":"nodekey:existing","user":{"id":"1","name":"alice"},
			 "reusable":false,"ephemeral":false,"used":false,"aclTags":[],
			 "createdAt":"2024-01-01T00:00:00Z","expiration":"2024-06-01T00:00:00Z"}
		]}`)
	})
	mux.HandleFunc("/api/v1/preauthkey/expire", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("/api/v1/node/register", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("key") == "hskey-authreq-failme00000000000000000000" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"registration key not found"}`)
			return
		}
		fmt.Fprintf(w, `{"node":{"id":"4","name":"new-machine","givenName":"new-machine","user":{"id":"1","name":%q}}}`, r.URL.Query().Get("user"))
	})

	client := headscaleclient.NewUnix(hstest.Socket(t, mux))
	return client, &requests, &policy, &lastTags
}

// activeOrgUser inserts a user with an "active" organization, wiring up
// h.hs to a fake headscale (used by
// handlers that still call resolveHeadscaleClient directly, e.g.
// Session/ListMembers's best-effort linking). Returns the (refreshed,
// org-attached) user, a client for the middleware-based handlers to
// attach via withHeadscaleClient, and the recorded requests slice.
func activeOrgUser(t *testing.T, h *Handlers, queries *dbpkg.TxQueries) (sqlcgen.User, *headscaleclient.Client, *[]*http.Request) {
	t.Helper()
	user, client, requests, _, _ := activeOrgUserWithPolicy(t, h, queries)
	return user, client, requests
}

// activeOrgUserWithPolicy is activeOrgUser plus a pointer to the fake
// server's in-memory ACL policy and to the tags from the most recent
// .../tags request, for tests exercising SetMachineTags's normalization
// and tagOwners auto-provisioning.
func activeOrgUserWithPolicy(t *testing.T, h *Handlers, queries *dbpkg.TxQueries) (sqlcgen.User, *headscaleclient.Client, *[]*http.Request, *string, *[]string) {
	t.Helper()
	user := insertUser(t, queries)

	orgID := xid.New().String()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{ID: orgID, Subdomain: "acme", Status: orgs.StatusActive})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{ID: org.ID, Status: orgs.StatusActive}); err != nil {
		t.Fatalf("UpdateOrganizationStatus: %v", err)
	}
	if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: org.ID,
		Role:           user.Role,
		ID:             user.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganizationAndRole: %v", err)
	}
	user, err = queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	client, requests, policy, lastTags := newFakeHeadscaleServer(t)
	h.hs = fakeHeadscaleClients{client: client}

	return user, client, requests, policy, lastTags
}

func newMachinesTestHandlers(t *testing.T) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	queries := newTestQueries(t)
	h, err := New(queries, "", "", nil, discardLogger(), config.DerpConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h, queries
}

func TestListMachinesReturnsNodesAndUsers(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user), client)
	rec := httptest.NewRecorder()
	h.ListMachines(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Machines []machineResponse      `json:"machines"`
		Users    []headscaleclient.User `json:"users"`
		Total    int                    `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 2 || len(body.Machines) != 2 {
		t.Errorf("got %d/%d machines, want 2/2", len(body.Machines), body.Total)
	}
	if len(body.Users) != 2 {
		t.Errorf("got %d users, want 2", len(body.Users))
	}
	if body.Machines[0].Internal {
		t.Errorf("first machine should not be a funnel machine")
	}
	if !body.Machines[1].Internal {
		t.Errorf("second machine should be a funnel machine")
	}
}

func TestListMachinesFiltersByQUserStatus(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	cases := []struct {
		name      string
		qs        string
		wantCount int
		wantIDs   []string
	}{
		{"q matches name", "q=node1", 1, []string{"1"}},
		{"user filter", "user=alice", 1, []string{"1"}},
		{"status online", "status=online", 1, []string{"1"}},
		{"status expired", "status=expired", 1, []string{"2"}},
		{"no filters", "", 2, []string{"1", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines?"+tc.qs, nil), user), client)
			rec := httptest.NewRecorder()
			h.ListMachines(rec, req)

			var body struct {
				Machines []headscaleclient.Node `json:"machines"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(body.Machines) != tc.wantCount {
				t.Fatalf("got %d machines, want %d", len(body.Machines), tc.wantCount)
			}
			for i, id := range tc.wantIDs {
				if body.Machines[i].ID != id {
					t.Errorf("machine[%d].ID = %q, want %q", i, body.Machines[i].ID, id)
				}
			}
		})
	}
}

func withPathValue(req *http.Request, key, value string) *http.Request {
	req.SetPathValue(key, value)
	return req
}

func TestRenameMachine(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"name":"new-name"}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/rename", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.RenameMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/rename/new-name") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}

func TestRenameMachineRejectsEmptyName(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"name":""}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/rename", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.RenameMachine(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestSetMachineTagsNormalization covers normalizeTags: the frontend's
// dialog claims it adds the "tag:" prefix automatically, so the backend
// must actually do it (defense in depth for any API caller), regardless of
// case, whitespace, or duplicate/empty entries.
func TestSetMachineTagsNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		wantTags []string
	}{
		{"already prefixed", []string{"tag:a", "tag:b"}, []string{"tag:a", "tag:b"}},
		{"missing prefix is added", []string{"a", "tag:b"}, []string{"tag:a", "tag:b"}},
		{"whitespace and case are normalized", []string{" Tag:A ", "B"}, []string{"tag:a", "tag:b"}},
		{"empty and duplicate entries are dropped", []string{"a", "tag:a", "", "  "}, []string{"tag:a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, queries := newMachinesTestHandlers(t)
			user, client, requests, _, lastTags := activeOrgUserWithPolicy(t, h, queries)

			payload, err := json.Marshal(map[string][]string{"tags": tt.input})
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", bytes.NewReader(payload)), user), client), "id", "1")
			rec := httptest.NewRecorder()
			h.SetMachineTags(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var tagsReqSeen bool
			for _, r := range *requests {
				if strings.HasSuffix(r.URL.Path, "/tags") {
					tagsReqSeen = true
				}
			}
			if !tagsReqSeen {
				t.Fatal("no request sent to .../tags")
			}
			if !slices.Equal(*lastTags, tt.wantTags) {
				t.Errorf("tags sent to headscale = %v, want %v", *lastTags, tt.wantTags)
			}
		})
	}
}

// TestSetMachineTagsProvisionsTagOwners covers ensureTagOwners: headscale
// rejects any tag missing from the ACL policy's tagOwners map (see
// hscontrol/state.State.SetNodeTags in headscale's source), and ownscale's
// seeded default policy never declares any tag -- so the handler must add
// missing tags to tagOwners itself before calling SetTags.
func TestSetMachineTagsProvisionsTagOwners(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _, policy, _ := activeOrgUserWithPolicy(t, h, queries)

	if strings.Contains(*policy, "tagOwners") {
		t.Fatalf("test fixture precondition failed: policy already has tagOwners: %s", *policy)
	}

	body := strings.NewReader(`{"tags":["tag:server"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var parsed struct {
		TagOwners map[string][]string `json:"tagOwners"`
	}
	if err := json.Unmarshal([]byte(*policy), &parsed); err != nil {
		t.Fatalf("policy sent to SetPolicy is not valid JSON: %v: %s", err, *policy)
	}
	owners, ok := parsed.TagOwners["tag:server"]
	if !ok || len(owners) == 0 {
		t.Fatalf("policy tagOwners missing/empty entry for tag:server: %+v", parsed.TagOwners)
	}
}

// TestSetMachineTagsSkipsPolicyUpdateWhenTagAlreadyOwned covers the case
// where every requested tag is already declared: ensureTagOwners must not
// call SetPolicy needlessly.
func TestSetMachineTagsSkipsPolicyUpdateWhenTagAlreadyOwned(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests, policy, _ := activeOrgUserWithPolicy(t, h, queries)
	*policy = `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}],"tagOwners":{"tag:server":["alice"]}}`

	body := strings.NewReader(`{"tags":["tag:server"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	for _, r := range *requests {
		if r.URL.Path == "/api/v1/policy" && r.Method == http.MethodPut {
			t.Fatal("SetPolicy should not be called when every tag is already in tagOwners")
		}
	}
}

// TestSetMachineTagsPreservesPolicyCommentsAndFormatting covers the actual
// bug: ensureTagOwners used to decode the policy into a map and re-encode
// it, which silently dropped comments and reordered/reformatted everything.
// It must now only patch in the new tagOwners entry, leaving the rest of
// the HuJSON document byte-for-byte untouched.
func TestSetMachineTagsPreservesPolicyCommentsAndFormatting(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, _, policy, _ := activeOrgUserWithPolicy(t, h, queries)
	original := "{\n" +
		"  // keep this comment\n" +
		"  \"acls\": [\n" +
		"    {\"action\": \"accept\", \"src\": [\"*\"], \"dst\": [\"*:*\"]},\n" +
		"  ],\n" +
		"}\n"
	*policy = original

	body := strings.NewReader(`{"tags":["tag:server"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(*policy, "// keep this comment") {
		t.Fatalf("policy sent to SetPolicy dropped the original comment: %s", *policy)
	}
	if !strings.Contains(*policy, `"action": "accept"`) {
		t.Fatalf("policy sent to SetPolicy reformatted the original acls entry: %s", *policy)
	}
	var parsed struct {
		TagOwners map[string][]string `json:"tagOwners"`
	}
	standardized, err := hujson.Standardize([]byte(*policy))
	if err != nil {
		t.Fatalf("policy sent to SetPolicy is not valid HuJSON: %v: %s", err, *policy)
	}
	if err := json.Unmarshal(standardized, &parsed); err != nil {
		t.Fatalf("policy sent to SetPolicy is not valid JSON: %v: %s", err, *policy)
	}
	if owners := parsed.TagOwners["tag:server"]; len(owners) == 0 {
		t.Fatalf("policy tagOwners missing/empty entry for tag:server: %+v", parsed.TagOwners)
	}
}

func TestSetMachineRoutes(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"routes":["10.0.0.0/24"]}`)
	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/routes", body), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/approve_routes") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}

func TestExpireMachine(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/expire", nil), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.ExpireMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/expire") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}

func TestDeleteMachine(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, client, requests := activeOrgUser(t, h, queries)

	route, err := queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
		ID:             xid.New().String(),
		OrganizationID: user.OrganizationID,
		Label:          "web",
		TargetNodeID:   "2",
		TargetPort:     8080,
		CreatedBy:      sql.NullString{String: user.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateFunnelRoute: %v", err)
	}

	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/machines/1", nil), user), client), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if last.Method != http.MethodDelete {
		t.Errorf("expected DELETE upstream, got %s", last.Method)
	}

	// Node "1" is owned by "alice" (not the funnel user), so deleting it
	// must not touch the org's funnel routes.
	remaining, err := queries.ListFunnelRoutesByOrg(t.Context(), user.OrganizationID)
	if err != nil {
		t.Fatalf("ListFunnelRoutesByOrg: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != route.ID {
		t.Errorf("got %d funnel routes after deleting a normal machine, want the original route untouched", len(remaining))
	}
}

func TestDeleteMachineCascadesFunnelRoutesForInternalMachine(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := insertUser(t, queries)

	orgID := xid.New().String()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{ID: orgID, Subdomain: "acme", Status: orgs.StatusActive})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{ID: org.ID, Status: orgs.StatusActive}); err != nil {
		t.Fatalf("UpdateOrganizationStatus: %v", err)
	}
	if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: org.ID,
		Role:           user.Role,
		ID:             user.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganizationAndRole: %v", err)
	}
	user, err = queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"nodes":[
			{"id":"1","name":"node1","givenName":"node1","ipAddresses":["100.64.0.1"],
			 "user":{"id":"1","name":"alice"},"online":true,"createdAt":"2024-01-01T00:00:00Z"},
			{"id":"2","name":"funnel-acme","givenName":"funnel-acme","ipAddresses":["100.64.0.2"],
			 "user":{"id":"2","name":%q},"online":true,"createdAt":"2024-01-01T00:00:00Z"}
		]}`, funnelproxy.FunnelHeadscaleUserName)
	})
	var deletedNodeID string
	mux.HandleFunc("/api/v1/node/2", func(w http.ResponseWriter, r *http.Request) {
		deletedNodeID = "2"
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})
	client := headscaleclient.NewUnix(hstest.Socket(t, mux))
	h.hs = fakeHeadscaleClients{client: client}

	for _, label := range []string{"web", "admin"} {
		if _, err := queries.CreateFunnelRoute(t.Context(), sqlcgen.CreateFunnelRouteParams{
			ID:             xid.New().String(),
			OrganizationID: org.ID,
			Label:          label,
			TargetNodeID:   "1",
			TargetPort:     8080,
			CreatedBy:      sql.NullString{String: user.ID, Valid: true},
		}); err != nil {
			t.Fatalf("CreateFunnelRoute(%q): %v", label, err)
		}
	}

	req := withPathValue(withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/machines/2", nil), user), client), "id", "2")
	rec := httptest.NewRecorder()
	h.DeleteMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if deletedNodeID != "2" {
		t.Errorf("expected node 2 to be deleted upstream, got %q", deletedNodeID)
	}

	remaining, err := queries.ListFunnelRoutesByOrg(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("ListFunnelRoutesByOrg: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("got %d funnel routes after deleting the internal funnel machine, want 0", len(remaining))
	}
}

func TestRegisterMachine(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantQuery  url.Values // only checked on 200
	}{
		{
			name:       "bare token",
			body:       `{"user":"alice","key":"abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusOK,
			wantQuery:  url.Values{"user": {"alice"}, "key": {"hskey-authreq-abcdefghijklmnopqrstuvwx"}},
		},
		{
			name:       "already prefixed key",
			body:       `{"user":"bob","key":"hskey-authreq-abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusOK,
			wantQuery:  url.Values{"user": {"bob"}, "key": {"hskey-authreq-abcdefghijklmnopqrstuvwx"}},
		},
		{
			name:       "full registration URL",
			body:       `{"user":"alice","key":"https://hs.example.test/register/abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusOK,
			wantQuery:  url.Values{"user": {"alice"}, "key": {"hskey-authreq-abcdefghijklmnopqrstuvwx"}},
		},
		{
			name:       "missing user",
			body:       `{"key":"abcdefghijklmnopqrstuvwx"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing key",
			body:       `{"user":"alice"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unrecognized key format",
			body:       `{"user":"alice","key":"not a key!!"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "headscale error propagates as 500",
			body:       `{"user":"alice","key":"hskey-authreq-failme00000000000000000000"}`,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, queries := newMachinesTestHandlers(t)
			user, client, requests := activeOrgUser(t, h, queries)

			req := withHeadscaleClient(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/register", strings.NewReader(tc.body)), user), client)
			rec := httptest.NewRecorder()
			h.RegisterMachine(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantQuery != nil {
				last := (*requests)[len(*requests)-1]
				if got := last.URL.Query(); got.Get("user") != tc.wantQuery.Get("user") || got.Get("key") != tc.wantQuery.Get("key") {
					t.Errorf("upstream query = %v, want %v", got, tc.wantQuery)
				}
			}
		})
	}
}
