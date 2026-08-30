package orgs

import (
	"context"
	"errors"
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
// feature existed), returning defaultAuthRestrictions instead.
func TestGetAuthRestrictionsDefaultsToEmptyWhenMissing(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)

	content, err := svc.GetAuthRestrictions(t.Context(), "nonexistent-org-id")
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != defaultAuthRestrictions {
		t.Fatalf("content = %q, want default", content)
	}
}

// TestSetAuthRestrictionsRejectsInvalidHuJSON verifies SetAuthRestrictions
// validates content before persisting or touching Docker.
func TestSetAuthRestrictionsRejectsInvalidHuJSON(t *testing.T) {
	svc, restarted := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	*restarted = nil

	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", "not valid hujson {{{"); err == nil {
		t.Fatal("SetAuthRestrictions: want error for invalid HuJSON, got nil")
	}

	if len(*restarted) != 0 {
		t.Fatalf("headscale restarted for invalid content: %v", *restarted)
	}
}

func TestSetAuthRestrictionsRollsBackOnDockerFailure(t *testing.T) {
	queries := newTestQueries(t)
	var failRestart bool
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerRestartFn: func(ctx context.Context, containerID string, options container.StopOptions) error {
			if failRestart {
				return errors.New("restart failed")
			}
			return nil
		},
	}
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	t.Cleanup(func() { ctl.Close() })

	svc := NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	failRestart = true
	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", `{"domains":["example.com"]}`); err == nil {
		t.Fatal("SetAuthRestrictions: want error when restart fails, got nil")
	}

	content, err := svc.GetAuthRestrictions(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != defaultAuthRestrictions {
		t.Fatalf("GetAuthRestrictions = %q, want the pre-failure default (rolled back)", content)
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

	newContent := `{"domains":["example.com"],"groups":["group:eng"],"users":["alice@example.com"]}`
	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", newContent); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	content, err := svc.GetAuthRestrictions(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != newContent {
		t.Fatalf("GetAuthRestrictions = %q, want %q", content, newContent)
	}
	if len(*restarted) != 1 || (*restarted)[0] != dockerctl.HSContainerName("acme") {
		t.Fatalf("restarted = %v, want [%q]", *restarted, dockerctl.HSContainerName("acme"))
	}
}

// TestSetAuthRestrictionsCanClearAllowlist verifies saving an empty
// allowlist after a previous non-empty save clears the stored one.
func TestSetAuthRestrictionsCanClearAllowlist(t *testing.T) {
	svc, _ := newAuthRestrictionsTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", `{"domains":["example.com"]}`); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}
	emptyContent := `{"domains":[],"groups":[],"users":[]}`
	if err := svc.SetAuthRestrictions(t.Context(), org.ID, "acme", emptyContent); err != nil {
		t.Fatalf("SetAuthRestrictions: %v", err)
	}

	content, err := svc.GetAuthRestrictions(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetAuthRestrictions: %v", err)
	}
	if content != emptyContent {
		t.Fatalf("GetAuthRestrictions = %q, want %q", content, emptyContent)
	}
}
