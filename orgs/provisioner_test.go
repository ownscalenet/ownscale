package orgs

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
)

// newTestService builds a Service backed by a temporary sqlite DB (see
// newTestQueries in orgs_test.go) and a fake Docker API that always
// succeeds, for exercising Provisioner without touching real Docker.
func newTestService(t *testing.T) *Service {
	t.Helper()
	queries := newTestQueries(t)
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerExecCreateFn: func(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error) {
			return container.ExecCreateResponse{ID: xid.New().String()}, nil
		},
		ContainerExecAttachFn: func(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
				_, _ = w.Write([]byte("headscale-api-key\n"))
				_ = serverConn.Close()
			}()
			return types.NewHijackedResponse(clientConn, ""), nil
		},
		ContainerExecInspectFn: func(ctx context.Context, execID string) (container.ExecInspect, error) {
			return container.ExecInspect{ExitCode: 0}, nil
		},
	}
	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })
	return NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
}

func TestProvisionerSignalProvisionsImmediately(t *testing.T) {
	svc := newTestService(t)
	org, err := svc.CreatePending(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}

	// A long poll interval means the org would never become active in
	// this test's lifetime via polling alone; only Signal should matter.
	p := NewProvisioner(svc, discardLogger(), time.Hour)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	p.Signal(org.ID)
	waitForStatus(t, svc, org.ID, "active")
}

func TestProvisionerPollPicksUpPendingOrganizations(t *testing.T) {
	svc := newTestService(t)
	org, err := svc.CreatePending(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}

	// No Signal call: only the poll loop should pick this org up.
	p := NewProvisioner(svc, discardLogger(), 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	waitForStatus(t, svc, org.ID, "active")
}

func TestProvisionerRunStopsOnContextCancel(t *testing.T) {
	svc := newTestService(t)
	p := NewProvisioner(svc, discardLogger(), time.Hour)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func waitForStatus(t *testing.T, svc *Service, orgID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		org, err := svc.queries.GetOrganization(t.Context(), orgID)
		if err != nil {
			t.Fatalf("GetOrganization: %v", err)
		}
		if org.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("organization status did not become %q within the deadline", want)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
