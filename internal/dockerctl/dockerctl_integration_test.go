//go:build integration

// Package dockerctl_test is for integration tests against a real docker backend.
// go test -tags=integration ./...
package dockerctl_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/google/uuid"

	"github.com/ownscalenet/ownscale/internal/dockerctl"
)

// requireDocker skips the test if no Docker daemon is reachable, so the
// suite degrades gracefully when run with the tag but without Docker available.
func requireDocker(t *testing.T) *client.Client {
	t.Helper()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	return cli
}

// randomSubdomain returns a short, unique, valid DNS-label subdomain for
// this test run, so repeated/parallel runs never collide on container names.
func randomSubdomain(t *testing.T) string {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	return "it" + id[:8]
}

func TestDockerctlIntegration(t *testing.T) {
	rawCli := requireDocker(t)

	var logBuf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	ctl, err := dockerctl.New(t.TempDir(), logger)
	if err != nil {
		t.Fatalf("dockerctl.New: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	const baseDomain = "it.test"
	subdomain := randomSubdomain(t)

	// Always attempt to remove the organization's containers/volume, even
	// if an assertion below fails mid-test, so repeated runs don't leave
	// orphaned containers on the host. The shared "os-net" network is
	// intentionally never removed here: it may be in use by real,
	// already-running organizations on this host (EnsureNetwork's
	// idempotency is exercised below instead).
	t.Cleanup(func() {
		// Must be context.Background() because t.Context() is already cancelled.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ctl.RemoveOrganization(ctx, subdomain); err != nil {
			t.Logf("cleanup: RemoveOrganization(%q): %v", subdomain, err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	t.Run("EnsureNetwork is idempotent", func(t *testing.T) {
		if err := ctl.EnsureNetwork(ctx); err != nil {
			t.Fatalf("EnsureNetwork (1st call): %v", err)
		}
		if err := ctl.EnsureNetwork(ctx); err != nil {
			t.Fatalf("EnsureNetwork (2nd call): %v", err)
		}
	})

	t.Run("CreateOrganization starts real containers", func(t *testing.T) {
		if _, err := ctl.CreateOrganization(ctx, subdomain, baseDomain); err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}

		hsName := dockerctl.HSContainerName(subdomain)
		hpName := dockerctl.HPContainerName(subdomain)

		for _, name := range []string{hsName, hpName} {
			info, err := rawCli.ContainerInspect(ctx, name)
			if err != nil {
				t.Fatalf("ContainerInspect(%q): %v", name, err)
			}
			if !info.State.Running {
				t.Errorf("container %q: State.Running = false, want true (state=%+v)", name, info.State)
			}
			if info.Config.Labels["ownscale.organization"] != "true" {
				t.Errorf("container %q: labels = %+v, want ownscale.organization=true", name, info.Config.Labels)
			}
			if info.Config.Labels["ownscale.subdomain"] != subdomain {
				t.Errorf("container %q: ownscale.subdomain label = %q, want %q", name, info.Config.Labels["ownscale.subdomain"], subdomain)
			}
			if _, ok := info.NetworkSettings.Networks["os-net"]; !ok {
				t.Errorf("container %q: not attached to the shared os-net network (networks=%+v)", name, info.NetworkSettings.Networks)
			}
		}
	})

	t.Run("StreamAllLogs observes real log output", func(t *testing.T) {
		if err := ctl.StreamAllLogs(ctx); err != nil {
			t.Fatalf("StreamAllLogs: %v", err)
		}

		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if logBuf.Len() > 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if logBuf.Len() == 0 {
			t.Error("expected at least one log line to have been streamed from the running containers")
		}
	})

	t.Run("stopping then StartOrganization restarts containers", func(t *testing.T) {
		// Note: this deliberately does NOT exercise Ctl.StopAll here.
		// StopAll stops *every* ownscale.organization=true container on
		// the Docker host by design (it's meant to be called once, at
		// process shutdown) — on a shared host that may also be running
		// other real organizations outside this test, calling it would
		// stop those too. Instead, stop only this test's own two
		// containers directly, then verify StartOrganization (which is
		// scoped to a single subdomain) brings them back up.
		hsName := dockerctl.HSContainerName(subdomain)
		hpName := dockerctl.HPContainerName(subdomain)
		for _, name := range []string{hsName, hpName} {
			timeout := 10
			if err := rawCli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout}); err != nil {
				t.Fatalf("ContainerStop(%q): %v", name, err)
			}
		}
		state, err := ctl.ContainerState(ctx, subdomain)
		if err != nil {
			t.Fatalf("ContainerState after stop: %v", err)
		}
		if state == "running" {
			t.Errorf("ContainerState after stop = %q, want not running", state)
		}

		if err := ctl.StartOrganization(ctx, subdomain); err != nil {
			t.Fatalf("StartOrganization: %v", err)
		}
		state, err = ctl.ContainerState(ctx, subdomain)
		if err != nil {
			t.Fatalf("ContainerState after StartOrganization: %v", err)
		}
		if state != "running" {
			t.Errorf("ContainerState after StartOrganization = %q, want %q", state, "running")
		}
	})

	t.Run("RemoveOrganization removes containers and volume", func(t *testing.T) {
		if err := ctl.RemoveOrganization(ctx, subdomain); err != nil {
			t.Fatalf("RemoveOrganization: %v", err)
		}

		for _, name := range []string{dockerctl.HSContainerName(subdomain), dockerctl.HPContainerName(subdomain)} {
			if _, err := rawCli.ContainerInspect(ctx, name); !cerrdefs.IsNotFound(err) {
				t.Errorf("ContainerInspect(%q) after RemoveOrganization: err = %v, want a not-found error", name, err)
			}
		}
		if _, err := rawCli.VolumeInspect(ctx, dockerctl.HPVolumeName(subdomain)); !cerrdefs.IsNotFound(err) {
			t.Errorf("VolumeInspect after RemoveOrganization: err = %v, want a not-found error", err)
		}
	})
}

// syncBuffer is a bytes.Buffer safe for concurrent writes, needed since
// streamLogs runs the log-streaming goroutines started by CreateOrganization
// and StreamAllLogs concurrently with the test's own goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}
