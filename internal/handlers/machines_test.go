package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ownscalenet/ownscale/internal/config"
	dbpkg "github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/headscaleclient"
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
		fmt.Fprint(w, `{"users":[{"id":"1","name":"alice"},{"id":"2","name":"bob"}]}`)
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

	orgID := uuid.Must(uuid.NewV7()).String()
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

func TestListMachinesNotConfiguredNoRegistry(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user := insertUser(t, queries)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.ListMachines(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		NotConfigured bool `json:"notConfigured"`
		Total         int  `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.NotConfigured {
		t.Error("expected notConfigured=true")
	}
}

func TestListMachinesNotConfiguredNoOrganization(t *testing.T) {
	// h.hs is set (registry configured) but the user has no organization.
	h, queries := newMachinesTestHandlers(t)
	h.hs = &fakeMachinesRegistry{}
	user := insertUser(t, queries)

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.ListMachines(rec, req)

	var body struct {
		NotConfigured bool `json:"notConfigured"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.NotConfigured {
		t.Error("expected notConfigured=true when the user has no organization")
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
