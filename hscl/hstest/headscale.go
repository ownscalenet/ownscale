package hstest

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	clientv1 "github.com/juanfont/headscale/gen/client/v1"
	"github.com/juanfont/headscale/hscontrol"
	hsconfig "github.com/juanfont/headscale/hscontrol/config"
	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/rs/zerolog"
	"tailscale.com/tailcfg"
)

// Headscale runs a real in-process headscale until the test ends and returns
// the path of its API socket, for hscl.NewUnix. Its logs are discarded.
func Headscale(t testing.TB) string {
	t.Helper()
	zerolog.SetGlobalLevel(zerolog.Disabled)

	// Not t.TempDir(): unix socket paths are limited to ~100 bytes.
	dir, err := os.MkdirTemp("", "hstest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	v4 := netip.MustParsePrefix("100.64.0.0/10")
	v6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	sock := filepath.Join(dir, "hs.sock")
	def := hsconfig.Default()
	app, err := hscontrol.NewHeadscale(&types.Config{
		ServerURL: "http://127.0.0.1",
		Addr:      "127.0.0.1:0",
		Noise:     types.NoiseConfig{PrivateKeyPath: filepath.Join(dir, "noise_private.key")},
		Prefixes:  types.PrefixesConfig{V4: &v4, V6: &v6, Allocation: types.IPAllocationStrategySequential},
		// A static map, so starting needs no network.
		DERP: types.DERPConfig{DERPMap: &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{
			900: {RegionID: 900, RegionCode: "test", Nodes: []*tailcfg.DERPNode{{Name: "900a", RegionID: 900, HostName: "derp.invalid"}}},
		}}},
		Database: types.DatabaseConfig{
			Type:   "sqlite3",
			Sqlite: types.SqliteConfig{Path: filepath.Join(dir, "db.sqlite"), WriteAheadLog: true},
		},
		Log:                  def.Log,
		TLS:                  def.TLS,
		PreAuthKeys:          def.PreAuthKeys,
		CLI:                  def.CLI,
		UnixSocket:           sock,
		UnixSocketPermission: 0o770,
		Policy:               types.PolicyConfig{Mode: types.PolicyMode("database")},
		Node: types.NodeConfig{
			Ephemeral: types.EphemeralConfig{InactivityTimeout: 2 * time.Minute},
		},
		Tuning: types.Tuning{
			BatchChangeDelay:               800 * time.Millisecond,
			BatcherWorkers:                 1,
			NodeMapSessionBufferedChanSize: 30,
			NodeStoreBatchSize:             100,
			RegisterCacheExpiration:        15 * time.Minute,
			RegisterCacheMaxEntries:        1024,
			// Node writes block until the next tick of this timeout.
			NodeStoreBatchTimeout: time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewHeadscale: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var serveErr error
	go func() {
		serveErr = app.Serve(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := socketClient(sock)
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get("http://local/api/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return sock
			}
		}
		select {
		case <-done:
			t.Fatalf("headscale exited before serving: %v", serveErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("headscale didn't serve on %s: %v", sock, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// PendingNode leaves a machine named name waiting to be registered to user,
// as `tailscale up --login-server=...` does, on the headscale serving sock.
// It returns the registration key hscl.Client.RegisterNode takes.
func PendingNode(t testing.TB, sock, user, name string, routes ...string) string {
	t.Helper()
	api, err := clientv1.NewClientWithResponses("http://local", clientv1.WithHTTPClient(socketClient(sock)))
	if err != nil {
		t.Fatal(err)
	}
	key := types.MustAuthID().String()
	resp, err := api.DebugCreateNodeWithResponse(t.Context(), clientv1.DebugCreateNodeJSONRequestBody{
		User:   &user,
		Key:    &key,
		Name:   &name,
		Routes: &routes,
	})
	if err != nil {
		t.Fatalf("DebugCreateNode: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("DebugCreateNode: %s: %s", resp.Status(), resp.Body)
	}
	return key
}

func socketClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}
