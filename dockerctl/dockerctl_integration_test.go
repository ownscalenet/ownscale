//go:build integration

// Package dockerctl_test is for integration tests against a real docker backend.
// go test -tags=integration ./...
package dockerctl_test

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/dockerctl"
)

// tarFileContents reads name's contents out of the tar stream r (as
// returned by client.CopyFromContainer).
func tarFileContents(r io.Reader, name string) (string, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", os.ErrNotExist
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) == name {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tr); err != nil {
				return "", err
			}
			return buf.String(), nil
		}
	}
}

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
	id := xid.New().String()
	return "it" + id[:8]
}

func TestDockerctlIntegration(t *testing.T) {
	rawCli := requireDocker(t)

	var logBuf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	dataDir := t.TempDir()
	ctl, err := dockerctl.New(dataDir, "", logger)
	if err != nil {
		t.Fatalf("dockerctl.New: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	const baseDomain = "it.test"
	subdomain := randomSubdomain(t)

	// Always attempt to remove the organization's containers/volume/network,
	// even if an assertion below fails mid-test, so repeated runs don't
	// leave orphaned resources on the host. The shared "os-net" network is
	// intentionally never removed here: it may be in use by real,
	// already-running organizations on this host (EnsureNetwork's
	// idempotency is exercised below instead).
	t.Cleanup(func() {
		// Must be t.Context() because t.Context() is already cancelled.
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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
		// OIDCClientCredentials is intentionally left empty here: headscale
		// (0.29.3) does eager, fatal OIDC discovery at startup whenever an
		// issuer is configured, and a reachable issuer for baseDomain only
		// exists when Caddy is actually running as part of the full
		// docker-compose stack (see ensureCaddyNetworkAlias), which this
		// daemon-only integration test doesn't spin up. The OIDC
		// issuer/network-alias wiring itself is covered by dockerfake-backed
		// unit tests instead (TestEnsureCaddyNetworkAlias*,
		// TestCreateOrganizationMountsCaddyDataVolumeAndSetsHeadplaneEnv).
		derpCfg := config.DerpConfig{
			Hostname:   "derp.it.test",
			STUNPort:   3478,
			RegionID:   900,
			RegionCode: "custom",
			RegionName: "Self-hosted DERP",
		}
		if _, err := ctl.CreateOrganization(ctx, subdomain, baseDomain, dockerctl.OIDCClientCredentials{}, derpCfg, dockerctl.DefaultACLPolicy); err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}

		hsName := dockerctl.HSContainerName(subdomain)
		hpName := dockerctl.HPContainerName(subdomain)

		reader, _, err := rawCli.CopyFromContainer(ctx, hsName, "/etc/headscale/derp.yaml")
		if err != nil {
			t.Fatalf("CopyFromContainer(derp.yaml): %v", err)
		}
		derpYAML, err := tarFileContents(reader, "derp.yaml")
		_ = reader.Close()
		if err != nil {
			t.Fatalf("read derp.yaml from container: %v", err)
		}
		if !strings.Contains(derpYAML, "derp.it.test") {
			t.Fatalf("derp.yaml in headscale container = %q, want it to reference the configured hostname", derpYAML)
		}

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
			if _, ok := info.NetworkSettings.Networks[dockerctl.OrgNetworkName(subdomain)]; !ok {
				t.Errorf("container %q: not attached to its own org network (networks=%+v)", name, info.NetworkSettings.Networks)
			}
			if _, ok := info.NetworkSettings.Networks["os-net"]; ok {
				t.Errorf("container %q: attached to the shared os-net network, want it isolated to its own org network only", name)
			}
		}

		// Network Management requires the headscale config file to be a
		// real, shared host file: verify both containers see identical
		// content at ConfigYAMLPath, backed by the same host file.
		hostConfigPath := filepath.Join(dataDir, "headscale-config", subdomain, "config.yaml")
		hostConfig, err := os.ReadFile(hostConfigPath)
		if err != nil {
			t.Fatalf("read shared config host file: %v", err)
		}

		for _, name := range []string{hsName, hpName} {
			reader, _, err := rawCli.CopyFromContainer(ctx, name, "/etc/headscale/config.yaml")
			if err != nil {
				t.Fatalf("CopyFromContainer(%q, config.yaml): %v", name, err)
			}
			cfg, err := tarFileContents(reader, "config.yaml")
			_ = reader.Close()
			if err != nil {
				t.Fatalf("read config.yaml from container %q: %v", name, err)
			}
			if cfg != string(hostConfig) {
				t.Errorf("container %q: /etc/headscale/config.yaml doesn't match the shared host file", name)
			}
		}

		hpInfo, err := rawCli.ContainerInspect(ctx, hpName)
		if err != nil {
			t.Fatalf("ContainerInspect(%q): %v", hpName, err)
		}
		if !slices.ContainsFunc(hpInfo.Mounts, func(m container.MountPoint) bool {
			return m.Source == "/var/run/docker.sock" && m.Destination == "/var/run/docker.sock"
		}) {
			t.Errorf("headplane container mounts = %+v, want a bind mount of the Docker socket", hpInfo.Mounts)
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

	t.Run("RemoveOrganization removes containers, host directories and network", func(t *testing.T) {
		if err := ctl.RemoveOrganization(ctx, subdomain); err != nil {
			t.Fatalf("RemoveOrganization: %v", err)
		}

		for _, name := range []string{dockerctl.HSContainerName(subdomain), dockerctl.HPContainerName(subdomain)} {
			if _, err := rawCli.ContainerInspect(ctx, name); !cerrdefs.IsNotFound(err) {
				t.Errorf("ContainerInspect(%q) after RemoveOrganization: err = %v, want a not-found error", name, err)
			}
		}
		for _, dir := range []string{
			filepath.Join(dataDir, "headscale", subdomain),
			filepath.Join(dataDir, "headplane", subdomain),
			filepath.Join(dataDir, "headscale-config", subdomain),
		} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("os.Stat(%q) after RemoveOrganization: err = %v, want a not-exist error", dir, err)
			}
		}
		if _, err := rawCli.NetworkInspect(ctx, dockerctl.OrgNetworkName(subdomain), network.InspectOptions{}); !cerrdefs.IsNotFound(err) {
			t.Errorf("NetworkInspect after RemoveOrganization: err = %v, want a not-found error", err)
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
