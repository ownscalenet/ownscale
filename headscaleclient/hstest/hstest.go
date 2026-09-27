// Package hstest serves fake headscale APIs on unix sockets, the way
// ownscale reaches its in-process headscale instances.
package hstest

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Socket serves h on a temporary unix socket until the test ends and
// returns the socket's path, for headscaleclient.NewUnix.
func Socket(t testing.TB, h http.Handler) string {
	t.Helper()
	// Not t.TempDir(): unix socket paths are limited to ~100 bytes, and
	// t.TempDir() embeds the (possibly long) test name.
	dir, err := os.MkdirTemp("", "hstest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sock := filepath.Join(dir, "hs.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln) //nolint:errcheck // returns http.ErrServerClosed on cleanup
	t.Cleanup(func() { srv.Close() })
	return sock
}
