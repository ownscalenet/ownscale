package orgs

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pressly/goose/v3"
	"golang.org/x/crypto/bcrypt"

	"github.com/ownscalenet/ownscale/internal/config"
	dbpkg "github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/dockerctl/dockerfake"
)

func newTestQueries(t *testing.T) *dbpkg.TxQueries {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	pool, err := dbpkg.Connect(dbPath)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	sub, err := fs.Sub(dbpkg.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("migrations sub fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, sub)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return dbpkg.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
			return container.ExecCreateResponse{ID: uuid.Must(uuid.NewV7()).String()}, nil
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
	hsCfg := copied[dockerctl.HSContainerName("acme")][strings.TrimPrefix("/etc/headscale/config.yaml", "/")]
	secretLine := findLine(hsCfg, `  client_secret: "`)
	secret := strings.TrimSuffix(strings.TrimPrefix(secretLine, `  client_secret: "`), `"`)
	if err := bcrypt.CompareHashAndPassword([]byte(clientRow.ClientSecretHash), []byte(secret)); err != nil {
		t.Fatalf("stored client secret hash does not match rendered config secret: %v", err)
	}
	if !strings.Contains(hsCfg, `issuer: "https://auth.example.com"`) {
		t.Fatalf("headscale config missing issuer:\n%s", hsCfg)
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
