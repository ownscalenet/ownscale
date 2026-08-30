package orgs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
)

// newACLTestService builds a Service backed by a dockerfake API that
// records every file uploaded via CopyToContainer (keyed by container ID,
// then archive path) and every container restarted, so tests can assert
// on both without a real Docker daemon.
func newACLTestService(t *testing.T) (svc *Service, copied map[string]map[string]string, restarted *[]string) {
	t.Helper()
	queries := newTestQueries(t)
	copiedFiles := make(map[string]map[string]string)
	var restartedContainers []string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		CopyToContainerFn: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			files, err := untar(content)
			if err != nil {
				return err
			}
			if copiedFiles[containerID] == nil {
				copiedFiles[containerID] = make(map[string]string)
			}
			maps.Copy(copiedFiles[containerID], files)
			return nil
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
	return svc, copiedFiles, &restartedContainers
}

// TestProvisionSeedsDefaultACLPolicy verifies provisioning an organization
// inserts the default ACL policy row and uploads it into the fresh
// headscale container.
func TestProvisionSeedsDefaultACLPolicy(t *testing.T) {
	svc, copied, _ := newACLTestService(t)

	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	content, err := svc.GetACLPolicy(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetACLPolicy: %v", err)
	}
	if content != dockerctl.DefaultACLPolicy {
		t.Fatalf("GetACLPolicy = %q, want default policy", content)
	}

	got := copied[dockerctl.HSContainerName("acme")][strings.TrimPrefix(dockerctl.PolicyConfigPath, "/")]
	if got != dockerctl.DefaultACLPolicy {
		t.Fatalf("uploaded acl policy = %q, want default policy", got)
	}
}

// TestGetACLPolicyFallsBackToDefaultWhenMissing verifies GetACLPolicy
// doesn't error for an organization with no orgconfigs row (e.g. one
// provisioned before this feature existed).
func TestGetACLPolicyFallsBackToDefaultWhenMissing(t *testing.T) {
	svc, _, _ := newACLTestService(t)

	content, err := svc.GetACLPolicy(t.Context(), "nonexistent-org-id")
	if err != nil {
		t.Fatalf("GetACLPolicy: %v", err)
	}
	if content != dockerctl.DefaultACLPolicy {
		t.Fatalf("content = %q, want default policy", content)
	}
}

// TestSetACLPolicyRejectsInvalidHuJSON verifies SetACLPolicy validates
// content before persisting or touching Docker.
func TestSetACLPolicyRejectsInvalidHuJSON(t *testing.T) {
	svc, copied, restarted := newACLTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Clear what provisioning itself already uploaded/restarted, so
	// assertions below only see what SetACLPolicy does.
	clear(copied)
	*restarted = nil

	if err := svc.SetACLPolicy(t.Context(), org.ID, "acme", "not valid hujson {{{"); err == nil {
		t.Fatal("SetACLPolicy: want error for invalid HuJSON, got nil")
	}

	if len(copied) != 0 {
		t.Fatalf("CopyToContainer called for invalid policy: %v", copied)
	}
	if len(*restarted) != 0 {
		t.Fatalf("headscale restarted for invalid policy: %v", *restarted)
	}
}

func TestSetACLPolicyRollsBackOnDockerFailure(t *testing.T) {
	queries := newTestQueries(t)
	var failRestart bool
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		CopyToContainerFn: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			return nil
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
	if err := svc.SetACLPolicy(t.Context(), org.ID, "acme", `{"acls":[]}`); err == nil {
		t.Fatal("SetACLPolicy: want error when restart fails, got nil")
	}

	content, err := svc.GetACLPolicy(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetACLPolicy: %v", err)
	}
	if content != dockerctl.DefaultACLPolicy {
		t.Fatalf("GetACLPolicy = %q, want the pre-failure default policy (rolled back)", content)
	}
}

// the new policy, uploads it into the running container, and restarts
// headscale so it reloads the policy.
func TestSetACLPolicyPersistsUploadsAndRestarts(t *testing.T) {
	svc, copied, restarted := newACLTestService(t)
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	clear(copied)
	*restarted = nil

	newPolicy := `{"acls":[{"action":"accept","src":["tag:test"],"dst":["*:*"]}]}`
	if err := svc.SetACLPolicy(t.Context(), org.ID, "acme", newPolicy); err != nil {
		t.Fatalf("SetACLPolicy: %v", err)
	}

	content, err := svc.GetACLPolicy(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetACLPolicy: %v", err)
	}
	if content != newPolicy {
		t.Fatalf("GetACLPolicy = %q, want %q", content, newPolicy)
	}

	got := copied[dockerctl.HSContainerName("acme")][strings.TrimPrefix(dockerctl.PolicyConfigPath, "/")]
	if got != newPolicy {
		t.Fatalf("uploaded acl policy = %q, want %q", got, newPolicy)
	}
	if len(*restarted) != 1 || (*restarted)[0] != dockerctl.HSContainerName("acme") {
		t.Fatalf("restarted = %v, want [%q]", *restarted, dockerctl.HSContainerName("acme"))
	}
}
