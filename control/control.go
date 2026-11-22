// Package control lets osa ask the running ownscale server to change state
// that only the server can change safely, over a unix socket in the data
// directory. Access is limited by the socket's file permissions.
package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/ownscalenet/ownscale/derrors"
)

// SocketPath returns the path of the control socket in dataDir.
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, "ownscale.sock")
}

// ErrNotRunning is returned by Client methods when no server listens on
// the control socket.
var ErrNotRunning = errors.New("ownscale isn't running")

// ErrNotFound is returned by Client.DeleteOrganization for an unknown
// organization.
var ErrNotFound = errors.New("not found")

// Handler serves the control API. deleteOrg deletes the organization with
// subdomain, returning sql.ErrNoRows if there is none.
func Handler(deleteOrg func(ctx context.Context, subdomain string) error, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /organizations/{subdomain}", func(w http.ResponseWriter, r *http.Request) {
		sub := r.PathValue("subdomain")
		err := deleteOrg(r.Context(), sub)
		switch {
		case err == nil:
			logger.Info("deleted organization through control socket", "org", sub)
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "organization not found", http.StatusNotFound)
		default:
			logger.Error("delete organization through control socket", "org", sub, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	return mux
}

// Client calls the control API of the server whose data directory is
// dataDir.
type Client struct {
	http *http.Client
}

// NewClient returns a Client for the control socket in dataDir.
func NewClient(dataDir string) *Client {
	path := SocketPath(dataDir)
	return &Client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}}
}

// DeleteOrganization asks the server to delete the organization with
// subdomain, with its data and backup.
func (c *Client) DeleteOrganization(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "c.DeleteOrganization(%q)", subdomain)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		"http://ownscale/organizations/"+url.PathEscape(subdomain), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if opErr := (*net.OpError)(nil); errors.As(err, &opErr) && opErr.Op == "dial" {
		return fmt.Errorf("%w: %w", ErrNotRunning, err)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("server: %s: %s", resp.Status, strings.TrimSpace(string(body)))
}
