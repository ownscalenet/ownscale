package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

// fakeMachinesRegistry is a machinesRegistry test double that always
// returns a *headscaleclient.Client pointed at an httptest.Server, instead
// of a real hs-<subdomain>:8080 Docker container.
type fakeMachinesRegistry struct {
	client *headscaleclient.Client
	err    error
}

func (f *fakeMachinesRegistry) Client(_ context.Context, _ string) (*headscaleclient.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

// newFakeHeadscaleServer builds an httptest.Server implementing enough of
// headscale's management API to exercise the /api/machines handlers, and
// a *headscaleclient.Client pointed at it.
func newFakeHeadscaleServer(t *testing.T) (*httptest.Server, *headscaleclient.Client, *[]*http.Request) {
	t.Helper()
	var requests []*http.Request

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node", func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"nodes":[
			{"id":"1","name":"node1","givenName":"node1","ipAddresses":["100.64.0.1"],
			 "user":{"id":"1","name":"alice"},"online":true,"tags":["tag:server"],
			 "approvedRoutes":["10.0.0.0/24"],"availableRoutes":["10.0.0.0/24"],
			 "createdAt":"2024-01-01T00:00:00Z"},
			{"id":"2","name":"node2","givenName":"node2","ipAddresses":["100.64.0.2"],
			 "user":{"id":"2","name":"bob"},"online":false,
			 "expiry":"2020-01-01T00:00:00Z","createdAt":"2024-01-01T00:00:00Z"}
		]}`)
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

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	client := headscaleclient.New(host, "test-key")
	return srv, client, &requests
}

// activeOrgUser inserts a user with an "active" organization, wiring up
// h.hs to a fakeMachinesRegistry backed by an httptest.Server fake
// headscale. Returns the (refreshed, org-attached) user and the recorded
// requests slice.
func activeOrgUser(t *testing.T, h *Handlers, queries *dbpkg.TxQueries) (sqlcgen.User, *[]*http.Request) {
	t.Helper()
	user := insertUser(t, queries)

	orgID := xid.New().String()
	org, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{ID: orgID, Subdomain: "acme"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if _, err := queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{ID: org.ID, Status: "active"}); err != nil {
		t.Fatalf("UpdateOrganizationStatus: %v", err)
	}
	if err := queries.SetUserOrganization(t.Context(), sqlcgen.SetUserOrganizationParams{
		OrganizationID: sql.NullString{String: org.ID, Valid: true},
		ID:             user.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganization: %v", err)
	}
	user, err = queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	_, client, requests := newFakeHeadscaleServer(t)
	h.hs = &fakeMachinesRegistry{client: client}

	return user, requests
}

func newMachinesTestHandlers(t *testing.T) (*Handlers, *dbpkg.TxQueries) {
	t.Helper()
	queries := newTestQueries(t)
	h := New(queries, nil, "", nil, discardLogger(), config.DerpConfig{})
	return h, queries
}

// TestListMachinesNotConfigured covers the two ways a request can end up
// "not configured": the user having no organization yet, and minting the
// org's headscale API key failing.
func TestListMachinesNotConfigured(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*Handlers, *dbpkg.TxQueries) sqlcgen.User
	}{
		{
			name: "no organization",
			setup: func(h *Handlers, queries *dbpkg.TxQueries) sqlcgen.User {
				h.hs = &fakeMachinesRegistry{}
				return insertUser(t, queries)
			},
		},
		{
			name: "mint key fails",
			setup: func(h *Handlers, queries *dbpkg.TxQueries) sqlcgen.User {
				user, _ := activeOrgUser(t, h, queries)
				h.hs = &fakeMachinesRegistry{err: errors.New("mint failed")}
				return user
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, queries := newMachinesTestHandlers(t)
			user := tc.setup(h, queries)

			req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
			rec := httptest.NewRecorder()
			h.ListMachines(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				NotConfigured bool `json:"notConfigured"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !body.NotConfigured {
				t.Error("expected notConfigured=true")
			}
		})
	}
}

func TestListMachinesReturnsNodesAndUsers(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.ListMachines(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		NotConfigured bool                   `json:"notConfigured"`
		Machines      []headscaleclient.Node `json:"machines"`
		Users         []headscaleclient.User `json:"users"`
		Total         int                    `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.NotConfigured {
		t.Fatal("expected notConfigured=false for an active organization")
	}
	if body.Total != 2 || len(body.Machines) != 2 {
		t.Errorf("got %d/%d machines, want 2/2", len(body.Machines), body.Total)
	}
	if len(body.Users) != 2 {
		t.Errorf("got %d users, want 2", len(body.Users))
	}
}

func TestListMachinesFiltersByQUserStatus(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	cases := []struct {
		name      string
		qs        string
		wantCount int
		wantIDs   []string
	}{
		{"q matches name", "q=node1", 1, []string{"1"}},
		{"user filter", "user=bob", 1, []string{"2"}},
		{"status online", "status=online", 1, []string{"1"}},
		{"status expired", "status=expired", 1, []string{"2"}},
		{"no filters", "", 2, []string{"1", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines?"+tc.qs, nil), user)
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
	user, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"name":"new-name"}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/rename", body), user), "id", "1")
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
	user, _ := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"name":""}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/rename", body), user), "id", "1")
	rec := httptest.NewRecorder()
	h.RenameMachine(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSetMachineTags(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"tags":["tag:a","tag:b"]}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/tags", body), user), "id", "1")
	rec := httptest.NewRecorder()
	h.SetMachineTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if !strings.HasSuffix(last.URL.Path, "/tags") {
		t.Errorf("unexpected upstream request path: %s", last.URL.Path)
	}
}

func TestSetMachineRoutes(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, requests := activeOrgUser(t, h, queries)

	body := strings.NewReader(`{"routes":["10.0.0.0/24"]}`)
	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/routes", body), user), "id", "1")
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
	user, requests := activeOrgUser(t, h, queries)

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/expire", nil), user), "id", "1")
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
	user, requests := activeOrgUser(t, h, queries)

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodDelete, "/api/machines/1", nil), user), "id", "1")
	rec := httptest.NewRecorder()
	h.DeleteMachine(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	last := (*requests)[len(*requests)-1]
	if last.Method != http.MethodDelete {
		t.Errorf("expected DELETE upstream, got %s", last.Method)
	}
}

func TestMachineActionNotConfiguredReturns503(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := insertUser(t, queries)

	req := withPathValue(withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/1/expire", nil), user), "id", "1")
	rec := httptest.NewRecorder()
	h.ExpireMachine(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestMachineActionMissingIDReturns400(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _ := activeOrgUser(t, h, queries)

	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines//expire", nil), user)
	rec := httptest.NewRecorder()
	h.ExpireMachine(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
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
			user, requests := activeOrgUser(t, h, queries)

			req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/register", strings.NewReader(tc.body)), user)
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

func TestRegisterMachineNotConfiguredReturns503(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := insertUser(t, queries)

	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/machines/register", strings.NewReader(`{"user":"alice","key":"abcdefghijklmnopqrstuvwx"}`)), user)
	rec := httptest.NewRecorder()
	h.RegisterMachine(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
