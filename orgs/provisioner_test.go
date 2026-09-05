package orgs

import (
	"context"
	"io"
	"log/slog"
	"net"
	"reflect"
	"sync"
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
	// Recovering 4 organizations sequentially (3 seeded + this test's own)
	// is slow under -race, so give it more headroom than a single Take needs.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	go func() { p.Run(ctx) }()

	expected := []string{
		"seed-aaaa",
		"seed-bbbb",
		"seed-cccc",
		pending.ID,
	}
	got := []string{}
	for range 3 + 1 { // 3 from seed, 1 from testcase
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

// TestProvisionerCircuitBreakerBacksOffAfterConsecutiveFailures checks
// that Run's replenish loop switches from the short retryBackoff to the
// long, fixed longBackoff once failureThreshold consecutive failures was reached.
func TestProvisionerCircuitBreakerBacksOffAfterConsecutiveFailures(t *testing.T) {
	queries := newTestQueries(t)

	// The DB is seeded (via migration 000008) with 6 pending organizations
	// that recoverPending provisions once, up front, outside of the
	// breaker-governed replenish loop. Remove them so only the
	// replenish-loop attempts below are tracked.
	seeded, err := queries.ListPendingOrganizations(t.Context())
	if err != nil {
		t.Fatalf("ListPendingOrganizations: %v", err)
	}
	for _, org := range seeded {
		if err := queries.DeleteOrganization(t.Context(), org.ID); err != nil {
			t.Fatalf("DeleteOrganization(%q): %v", org.ID, err)
		}
	}

	// attempt i (0-indexed, in creation order) fails if failAt[i] is true.
	// fail, fail, succeed, fail, succeed: exercises the breaker tripping
	// after 2 consecutive failures, then resetting after the success.
	failAt := []bool{true, true, false, true, false}

	var mu sync.Mutex
	var attemptTimes []time.Time
	healthyContainer := make(map[string]bool)

	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, cfg *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			i := len(attemptTimes)
			attemptTimes = append(attemptTimes, time.Now())
			healthyContainer[containerName] = i >= len(failAt) || !failAt[i]
			return container.CreateResponse{ID: containerName}, nil
		},
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			mu.Lock()
			healthy := healthyContainer[containerID]
			mu.Unlock()
			status := container.Healthy
			if !healthy {
				status = container.Unhealthy
			}
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					State: &container.State{Status: "running", Health: &container.Health{Status: status}},
				},
			}, nil
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

	ctl, err := dockerctl.NewWithClient(api, t.TempDir(), "", discardLogger())
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	t.Cleanup(func() { _ = ctl.Close() })

	svc := NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})

	const (
		retryBackoff = 30 * time.Millisecond
		// bcrypt function with -race is very slow.
		longBackoff = 5 * time.Second
	)
	p := NewProvisioner(svc, discardLogger(), retryBackoff, 1)
	p.failureThreshold = 2
	p.longBackoff = longBackoff

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	go func() { p.Run(ctx) }()

	if _, err := p.Take(ctx); err != nil {
		t.Fatalf("first Take (after the breaker trips and resets): %v", err)
	}
	if _, err := p.Take(ctx); err != nil {
		t.Fatalf("second Take (after the breaker resets): %v", err)
	}

	mu.Lock()
	times := append([]time.Time(nil), attemptTimes...)
	mu.Unlock()
	if len(times) < len(failAt) {
		t.Fatalf("got %d provisioning attempts, want at least %d", len(times), len(failAt))
	}

	gap := func(i, j int) time.Duration { return times[j].Sub(times[i]) }

	// attempt 0 -> 1: only 1 consecutive failure so far, short backoff.
	if g := gap(0, 1); g >= longBackoff {
		t.Errorf("gap between attempts 1 and 2 = %s, want well under longBackoff (%s): breaker should not have tripped yet", g, longBackoff)
	}
	// attempt 1 -> 2: 2 consecutive failures reached failureThreshold, so
	// the breaker should have tripped to longBackoff.
	if g := gap(1, 2); g < longBackoff {
		t.Errorf("gap between attempts 2 and 3 = %s, want at least longBackoff (%s): breaker should have tripped", g, longBackoff)
	}
	// attempt 3 -> 4: attempt 2 succeeded and reset the failure counter, so
	// the single new failure (attempt 3) uses the short backoff again.
	if g := gap(3, 4); g >= longBackoff {
		t.Errorf("gap between attempts 4 and 5 = %s, want well under longBackoff (%s): breaker should have reset after the success", g, longBackoff)
	}
}
