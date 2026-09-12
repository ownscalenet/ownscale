package orgs

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rs/xid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/config"
	dbpkg "github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/dbtest"
	"github.com/ownscalenet/ownscale/dockerctl"
	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
	"github.com/ownscalenet/ownscale/headscaleclient"
)

// newFakePolicyClient returns a headscaleclient.Client pointed at an
// httptest.Server implementing headscale's policy endpoint (GET/PUT
// /api/v1/policy), accepting any policy content unconditionally. Used to
// override dockerctl.Ctl.MintClientFn in tests so CreateOrganization's
// default-policy seeding step doesn't attempt a real network round trip to
// a container hostname that doesn't actually exist under dockerfake.
func newFakePolicyClient(t *testing.T) *headscaleclient.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/policy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"policy":""}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	return headscaleclient.New(host, "test-api-key")
}

// newTestQueries returns queries backed by a copy of the shared, already
// migrated dbtest template (see db/dbtest), avoiding a full goose run per
// test.
func newTestQueries(t *testing.T) *dbpkg.TxQueries {
	t.Helper()
	_, queries := dbtest.New(t)
	return queries
}

func TestServiceCreateCreatesOIDCClient(t *testing.T) {
	queries := newTestQueries(t)
	copied := make(map[string]map[string]string)
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		CopyToContainerFn: func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
			files, err := untar(content)
			if err != nil {
				return err
			}
			if copied[containerID] == nil {
				copied[containerID] = make(map[string]string)
			}
			maps.Copy(copied[containerID], files)
			return nil
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
	dataDir := t.TempDir()
	ctl, err := dockerctl.NewWithClient(api, dataDir, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	ctl.MintClientFn = func(ctx context.Context, subdomain string) (*headscaleclient.Client, error) {
		return newFakePolicyClient(t), nil
	}
	t.Cleanup(func() { _ = ctl.Close() })

	svc := NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	org, err := svc.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	clientRow, err := queries.GetOIDCClientByOrganizationID(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOIDCClientByOrganizationID: %v", err)
	}
	if !strings.HasPrefix(clientRow.ClientID, "os_acme_") {
		t.Fatalf("client id = %q, want os_acme_*", clientRow.ClientID)
	}
	var redirectURIs []string
	if err := json.Unmarshal([]byte(clientRow.RedirectUris), &redirectURIs); err != nil {
		t.Fatalf("Unmarshal redirect uris: %v", err)
	}
	wantRedirects := []string{
		"https://acme.example.com/oidc/callback",
		"https://acme.example.com/admin/oidc/callback",
	}
	if strings.Join(redirectURIs, ",") != strings.Join(wantRedirects, ",") {
		t.Fatalf("redirect uris = %v, want %v", redirectURIs, wantRedirects)
	}
	hsCfgBytes, err := os.ReadFile(filepath.Join(dataDir, "headscale-config", "acme", "config.yaml"))
	if err != nil {
		t.Fatalf("read headscale config file: %v", err)
	}
	hsCfg := string(hsCfgBytes)
	secretLine := findLine(hsCfg, `  client_secret: "`)
	secret := strings.TrimSuffix(strings.TrimPrefix(secretLine, `  client_secret: "`), `"`)
	if err := bcrypt.CompareHashAndPassword([]byte(clientRow.ClientSecretHash), []byte(secret)); err != nil {
		t.Fatalf("stored client secret hash does not match rendered config secret: %v", err)
	}
	if !strings.Contains(hsCfg, `issuer: "https://auth.example.com"`) {
		t.Fatalf("headscale config missing issuer:\n%s", hsCfg)
	}
}

func TestServiceProvisionRemovesContainerOnFailure(t *testing.T) {
	queries := newTestQueries(t)
	var removed []string
	api := &dockerfake.API{
		ContainerCreateFn: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: containerName}, nil
		},
		// Simulate the container never becoming healthy, so
		// dockerctl.Ctl.waitForHealthy (and thus CreateOrganization) fails.
		ContainerInspectFn: func(ctx context.Context, containerID string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					State: &container.State{
						Status: "running",
						Health: &container.Health{Status: container.Unhealthy},
					},
				},
			}, nil
		},
		ContainerRemoveFn: func(ctx context.Context, containerID string, options container.RemoveOptions) error {
			removed = append(removed, containerID)
			return nil
		},
	}
	dataDir := t.TempDir()
	ctl, err := dockerctl.NewWithClient(api, dataDir, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("dockerctl.NewWithClient: %v", err)
	}
	t.Cleanup(func() { ctl.Close() })

	svc := NewService(queries, ctl, "example.com", "https://auth.example.com", config.DerpConfig{})
	org, err := svc.CreatePending(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if err := svc.Provision(t.Context(), org.ID); err == nil {
		t.Fatal("Provision: want error when the container never becomes healthy, got nil")
	}

	got, err := queries.GetOrganization(t.Context(), org.ID)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("org.Status = %q, want %q", got.Status, StatusFailed)
	}

	wantContainer := dockerctl.HSContainerName("acme")
	if !slices.Contains(removed, wantContainer) {
		t.Errorf("ContainerRemove calls = %v, want it to include %q (no zombie container should survive a failed Provision)", removed, wantContainer)
	}
}

func untar(r io.Reader) (map[string]string, error) {
	tr := tar.NewReader(r)
	files := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			return nil, err
		}
		files[hdr.Name] = buf.String()
	}
}

func findLine(s, prefix string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
