package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/internal/auth"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/dockerctl/dockerfake"
)

// withTestUser returns req with user attached to its context, as
// auth.RequireAuthAPI middleware would for an authenticated request.
func withTestUser(req *http.Request, user sqlcgen.User) *http.Request {
	return req.WithContext(auth.WithUser(req.Context(), &user))
}

func TestCreateOrganizationNotConfigured(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestCreateOrganizationUnauthenticated(t *testing.T) {
	h, _ := newTestHandlers(t, true)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestCreateOrganizationAlreadyHasOrganization(t *testing.T) {
	h, queries := newTestHandlers(t, true)
	user := insertUser(t, queries)
	existingOrg, err := h.orgs.Create(t.Context(), "existing-org")
	if err != nil {
		t.Fatalf("orgs.Create: %v", err)
	}
	if err := queries.SetUserOrganization(t.Context(), sqlcgen.SetUserOrganizationParams{
		OrganizationID: sql.NullString{String: existingOrg.ID, Valid: true},
		ID:             user.ID,
	}); err != nil {
		t.Fatalf("SetUserOrganization: %v", err)
	}
	user, err = queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	req = withTestUser(req, user)
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestCreateOrganizationInvalidJSON(t *testing.T) {
	h, queries := newTestHandlers(t, true)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`not json`))
	req = withTestUser(req, user)
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateOrganizationInvalidSubdomain(t *testing.T) {
	h, queries := newTestHandlers(t, true)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"Not Valid!"}`))
	req = withTestUser(req, user)
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateOrganizationSuccess(t *testing.T) {
	h, queries := newTestHandlers(t, true)
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	req = withTestUser(req, user)
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "acme") {
		t.Errorf("body = %q, want it to contain the created subdomain", rec.Body.String())
	}

	updated, err := queries.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !updated.OrganizationID.Valid {
		t.Error("OrganizationID: want it set after CreateOrganization, got unset")
	}
}

func TestCreateOrganizationDockerFailure(t *testing.T) {
	api := &dockerfake.API{
		ContainerCreateFn: func(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error) {
			return container.CreateResponse{}, bytes.ErrTooLarge
		},
	}
	h, queries := newTestHandlers(t, false)
	ctl := newTestCtl(t, api)
	h = New(h.queries, ctl, testBaseDomain, nil, discardLogger())
	user := insertUser(t, queries)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	req = withTestUser(req, user)
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	// The DB insert (status "pending") happens inline and succeeds
	// regardless of Docker; only the (deferred) provisioning step fails.
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	org, err := queries.GetOrganizationBySubdomain(t.Context(), "acme")
	if err != nil {
		t.Fatalf("GetOrganizationBySubdomain: %v", err)
	}
	if org.Status != "pending" {
		t.Fatalf("status = %q, want %q", org.Status, "pending")
	}

	// Simulate the background provisioner picking up the signal.
	if err := h.orgs.Provision(t.Context(), org.ID); err == nil {
		t.Fatal("Provision: want error from failing Docker call, got nil")
	}

	org, err = queries.GetOrganization(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if org.Status != "failed" {
		t.Errorf("status after failed provisioning = %q, want %q", org.Status, "failed")
	}
}
