package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/internal/dockerctl/dockerfake"
)

func TestCreateOrganizationNotConfigured(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestCreateOrganizationInvalidJSON(t *testing.T) {
	h, _ := newTestHandlers(t, true)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateOrganizationInvalidSubdomain(t *testing.T) {
	h, _ := newTestHandlers(t, true)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"Not Valid!"}`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateOrganizationSuccess(t *testing.T) {
	h, _ := newTestHandlers(t, true)

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "acme") {
		t.Errorf("body = %q, want it to contain the created subdomain", rec.Body.String())
	}
}

func TestCreateOrganizationDockerFailure(t *testing.T) {
	api := &dockerfake.API{
		ContainerCreateFn: func(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *ocispec.Platform, string) (container.CreateResponse, error) {
			return container.CreateResponse{}, bytes.ErrTooLarge
		},
	}
	h, _ := newTestHandlers(t, false)
	ctl := newTestCtl(t, api)
	h = New(h.queries, ctl, testBaseDomain, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/organizations", strings.NewReader(`{"subdomain":"acme"}`))
	rec := httptest.NewRecorder()
	h.CreateOrganization(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
