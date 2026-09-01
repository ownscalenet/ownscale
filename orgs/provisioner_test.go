package orgs

import (
	"context"
	"io"
	"log/slog"
	"net"
	"reflect"
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
	"github.com/ownscalenet/ownscale/headscaleclient"
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
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	t.Cleanup(func() { _ = ctl.Close() })
	return NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
}

func TestProvisionerTakeReturnsProvisionedOrganization(t *testing.T) {
	svc := newTestService(t)
	p := NewProvisioner(svc, discardLogger(), time.Hour, 1)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	org, err := p.Take(ctx)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if org.Status != "active" {
		t.Errorf("org.Status = %q, want %q", org.Status, "active")
	}
}

// TestProvisionerReplenishesAfterTake checks that removing an organization
// from the pool causes another to be produced to take its place, up to the
// configured capacity.
func TestProvisionerReplenishesAfterTake(t *testing.T) {
	svc := newTestService(t)
	p := NewProvisioner(svc, discardLogger(), time.Hour, 1)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() { _ = p.Run(ctx) }()

	first, err := p.Take(ctx)
	if err != nil {
		t.Fatalf("first Take: %v", err)
	}
	second, err := p.Take(ctx)
	if err != nil {
		t.Fatalf("second Take: %v", err)
	}
	if first.ID == second.ID {
		t.Errorf("second Take returned the same organization as the first: %q", first.ID)
	}
	if second.Status != "active" {
		t.Errorf("second.Status = %q, want %q", second.Status, "active")
	}
}

// TestProvisionerRecoversPendingOnStart checks that an organization left
// "pending" (e.g. by a process that crashed between creating and pooling
// it) is provisioned and pooled as soon as Run starts, without waiting for
// the replenish loop to generate a brand new one.
func TestProvisionerRecoversPendingOnStart(t *testing.T) {
	svc := newTestService(t)
	pending, err := svc.CreatePending(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}

	p := NewProvisioner(svc, discardLogger(), time.Hour, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { p.Run(ctx) }()

	expected := []string{
		"seed-aaaa",
		"seed-bbbb",
		"seed-cccc",
		"seed-dddd",
		"seed-eeee",
		"seed-ffff",
		pending.ID,
	}
	got := []string{}
	for range 6 + 1 { // 6 from seed, 1 from testcase
		org, err := p.Take(ctx)
		if err != nil {
			t.Fatalf("Take: %v", err)
		}
		if org.Status != "active" {
			t.Errorf("org.Status = %q, want %q", org.Status, "active")
		}
		got = append(got, org.ID)
	}
	if !reflect.DeepEqual(expected, got) {
		t.Errorf("Take returned orgs %q, want the recovered pending orgs %q", got, expected)
	}
}

func TestProvisionerTakeReturnsContextErrorWhenPoolEmpty(t *testing.T) {
	svc := newTestService(t)
	// A Provisioner that's never Run: the pool never fills, so Take must
	// return once ctx is done instead of blocking forever.
	p := NewProvisioner(svc, discardLogger(), time.Hour, 1)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Take(ctx); err == nil {
		t.Fatal("Take: want error when the pool never fills and ctx is done, got nil")
	}
}

func TestProvisionerRunStopsOnContextCancel(t *testing.T) {
	svc := newTestService(t)
	p := NewProvisioner(svc, discardLogger(), time.Hour, 1)

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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
