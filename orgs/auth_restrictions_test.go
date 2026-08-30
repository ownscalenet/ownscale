package orgs

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
)

// newAuthRestrictionsTestService builds a Service backed by a dockerfake
// API and a real temp dataDir (so SetOIDCAllowlist has an actual
// config.yaml on disk to patch, written by CreateOrganization), recording
// every container restarted so tests can assert on it without a real
// Docker daemon.
func newAuthRestrictionsTestService(t *testing.T) (svc *Service, restarted *[]string) {
	t.Helper()
	queries := newTestQueries(t)
	var restartedContainers []string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerRestartFn: func(ctx context.Context, containerID string, options container.StopOptions) error {
			restartedContainers = append(restartedContainers, containerID)
			return nil
		},
	}
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	svc = NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	return svc, &restartedContainers
}

// TestGetAuthRestrictionsDefaultsToEmptyWhenMissing verifies
// GetAuthRestrictions doesn't error for an organization with no
// orgconfigs row (e.g. a brand new one, or one provisioned before this
// feature existed), returning "no restriction" (empty slices) instead.
func TestGetAuthRestrictionsDefaultsToEmptyWhenMissing(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)

	domains, groups, users, err := svc.GetAuthRestrictions(t.Context(), "nonexistent-org-id")
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if len(domains) != 0 || len(groups) != 0 || len(users) != 0 {
		t.Fatalf("domains=%v groups=%v users=%v, want all empty", domains, groups, users)
	}
}

// TestSetAuthRestrictionsPersistsPatchesAndRestarts verifies
// SetAuthRestrictions stores the new allowlist, patches it into the
// running headscale container's config.yaml, and restarts headscale so it
// reloads it.
func TestSetAuthRestrictionsPersistsPatchesAndRestarts(t *testing.T) {
	svc, restarted := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	*restarted = nil

	wantDomains := []string{"example.com"}
	wantGroups := []string{"group:eng"}
	wantUsers := []string{"alice@example.com"}
	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", wantDomains, wantGroups, wantUsers); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	domains, groups, users, err := svc.GetAuthRestrictions(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("domains = %v, want %v", domains, wantDomains)
	}
	if len(groups) != 1 || groups[0] != "group:eng" {
		t.Fatalf("groups = %v, want %v", groups, wantGroups)
	}
	if len(users) != 1 || users[0] != "alice@example.com" {
		t.Fatalf("users = %v, want %v", users, wantUsers)
	}
	if len(*restarted) != 1 || (*restarted)[0] != dockerctl.HSContainerName("acme") {
		t.Fatalf("restarted = %v, want [%q]", *restarted, dockerctl.HSContainerName("acme"))
	}
}

// TestSetAuthRestrictionsCanClearAllowlist verifies passing empty slices
// after a previous non-empty save clears the stored allowlist.
func TestSetAuthRestrictionsCanClearAllowlist(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", []string{"example.com"}, nil, nil); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}
	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", nil, nil, nil); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	domains, groups, users, err := svc.GetAuthRestrictions(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if len(domains) != 0 || len(groups) != 0 || len(users) != 0 {
		t.Fatalf("domains=%v groups=%v users=%v, want all empty after clearing", domains, groups, users)
	}
}
