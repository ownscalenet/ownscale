package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/db/sqlcgen"
	"github.com/ownscalenet/ownscale/headscaleclient"
	"github.com/ownscalenet/ownscale/orgs"
)

// TestRequireActiveOrgShortCircuits covers RequireActiveOrg's guard: an
// unauthenticated caller gets 401, and a non-active (e.g. still "ready",
// pooled but unassigned) organization gets 409+notConfigured.
func TestRequireActiveOrgShortCircuits(t *testing.T) {
	h, queries := newTestHandlers(t)

	pendingOrg, err := queries.CreateOrganization(t.Context(), sqlcgen.CreateOrganizationParams{
		ID:        xid.New().String(),
		Subdomain: "acme",
		Status:    orgs.StatusReady,
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	pendingUser := insertUser(t, queries)
	if err := queries.SetUserOrganizationAndRole(t.Context(), sqlcgen.SetUserOrganizationAndRoleParams{
		OrganizationID: pendingOrg.ID,
		Role:           RoleOwner,
		ID:             pendingUser.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganizationAndRole: %v", err)
	}
	pendingUser, err = queries.GetUserByID(t.Context(), pendingUser.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	cases := []struct {
		name       string
		req        *http.Request
		wantStatus int
	}{
		{
			name:       "unauthenticated",
			req:        httptest.NewRequest(http.MethodGet, "/api/access-control", nil),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "pending organization",
			req:        withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), pendingUser),
			wantStatus: http.StatusConflict,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled := false
			next := func(http.ResponseWriter, *http.Request) { nextCalled = true }

			rec := httptest.NewRecorder()
			h.RequireActiveOrg(next)(rec, tc.req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d, body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if nextCalled {
				t.Error("next was called, want short-circuit")
			}
		})
	}
}

func TestRequireActiveOrgAllowsUpdatePending(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleOwner)
	owner := users[0]

	if _, err := h.queries.UpdateOrganizationStatus(t.Context(), sqlcgen.UpdateOrganizationStatusParams{
		Status: orgs.StatusUpdatePending,
		ID:     org.ID,
	}); err != nil {
		t.Fatalf("UpdateOrganizationStatus: %v", err)
	}

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/auth-restrictions", nil), owner)
	rec := httptest.NewRecorder()
	h.RequireActiveOrg(next)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !nextCalled {
		t.Error("next was not called, want it to be called for an update_pending org")
	}
}

func TestRequireActiveOrgCallsNextForActiveOrg(t *testing.T) {
	h, org, users := newAccessControlTestHandlers(t, RoleMember)
	member := users[0]

	var gotCaller *sqlcgen.User
	var gotOrg sqlcgen.Organization
	next := func(w http.ResponseWriter, r *http.Request) {
		gotCaller, gotOrg, _ = orgFromContext(r)
		w.WriteHeader(http.StatusOK)
	}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/access-control", nil), member)
	rec := httptest.NewRecorder()
	h.RequireActiveOrg(next)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotCaller == nil || gotCaller.ID != member.ID {
		t.Errorf("context caller = %+v, want %+v", gotCaller, member)
	}
	if gotOrg.ID != org.ID {
		t.Errorf("context org = %+v, want %+v", gotOrg, org)
	}
}

// TestRequireHeadscaleClientShortCircuits covers RequireHeadscaleClient's
// guard: a mint-key failure short-circuits with 409+notConfigured without
// calling next.
func TestRequireHeadscaleClientShortCircuits(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _, _ := activeOrgUser(t, h, queries)
	h.hs = headscaleclient.NewRegistry(&fakeClientMinter{err: errors.New("mint failed")})

	nextCalled := false
	next := func(http.ResponseWriter, *http.Request) { nextCalled = true }

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(next)(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
	if nextCalled {
		t.Error("next was called, want short-circuit")
	}
}

func TestRequireHeadscaleClientCallsNextWithClient(t *testing.T) {
	h, queries := newMachinesTestHandlers(t)
	user, _, _ := activeOrgUser(t, h, queries)

	var gotClient bool
	next := func(w http.ResponseWriter, r *http.Request) {
		_, gotClient = headscaleClientFromContext(r)
		w.WriteHeader(http.StatusOK)
	}

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/machines", nil), user)
	rec := httptest.NewRecorder()
	h.RequireHeadscaleClient(next)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !gotClient {
		t.Error("expected a client attached to the request context")
	}
}
