package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/config"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/logging"
	"github.com/ownscalenet/ownscale/orgs"
)

// loadConfigAndLogger reads the config from OWNSCALE_* environment
// variables and builds a logger from its Log settings. It's called
// at the start of every organization subcommand's Exec, since each
// subcommand runs as its own short-lived process invocation.
func loadConfigAndLogger() (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}

	// Use less verbose level in CLI.
	// DEBUG prints logs for queries and that makes the CLI output ugly.
	if strings.ToUpper(cfg.Log.Level) == "DEBUG" {
		cfg.Log.Level = "INFO"
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		return nil, nil, err
	}
	return cfg, logger, nil
}

// orgResources bundles the dependencies shared by osa subcommands: the
// config and logger, and an open SQLite connection/query handle. Business
// logic functions take these already-open dependencies directly so tests can
// exercise them against a real database.
type orgResources struct {
	cfg     *config.Config
	logger  *slog.Logger
	queries *db.TxQueries
}

// newOrgResources loads the config and logger and opens the SQLite database.
// The returned cleanup func closes what was opened; it's always non-nil and
// safe to defer, even if newOrgResources returns an error.
func newOrgResources() (_ *orgResources, cleanup func(), err error) {
	var closers []func() error
	cleanup = func() {
		for _, closer := range slices.Backward(closers) {
			closer() //nolint:errcheck // best-effort close on process exit
		}
	}

	cfg, logger, err := loadConfigAndLogger()
	if err != nil {
		return nil, cleanup, err
	}
	pool, err := db.Connect(cfg.Database())
	if err != nil {
		return nil, cleanup, err
	}
	closers = append(closers, pool.Close)
	return &orgResources{cfg: cfg, logger: logger, queries: db.New(pool, logger)}, cleanup, nil
}

// newOrganizationCommand builds the "organization" subcommand group
// (list), parented under rootFlags.
func newOrganizationCommand(rootFlags *ff.FlagSet) *ff.Command {
	organizationFlags := ff.NewFlagSet("organization").SetParent(rootFlags)
	organizationCmd := &ff.Command{
		Name:      "organization",
		Usage:     "osa organization SUBCOMMAND ...",
		ShortHelp: "inspect organizations",
		Flags:     organizationFlags,
	}

	organizationCmd.Subcommands = append(organizationCmd.Subcommands,
		newOrganizationListCommand(organizationFlags),
	)
	return organizationCmd
}

func newOrganizationListCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("organization list").SetParent(parent)
	return &ff.Command{
		Name:      "list",
		Usage:     "osa organization list [FLAGS]",
		ShortHelp: "list organizations",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa organization list")
			}
			res, cleanup, err := newOrgResources()
			defer cleanup()
			if err != nil {
				return err
			}
			probe := func(ctx context.Context, subdomain string) probeResult {
				return probeHeadscale(ctx, hscl.NewUnix(orgs.SocketPath(res.cfg.DataDir, subdomain)))
			}
			return listOrganizations(ctx, os.Stdout, res.queries, probe)
		},
	}
}

// probeTimeout bounds how long probeHeadscale waits for an answer.
const probeTimeout = 2 * time.Second

// probeResult is what probeHeadscale learns about an organization's headscale.
type probeResult struct {
	status   string
	machines int
	online   int
}

// probeHeadscale reports whether the headscale behind client answers and how
// many machines it has. The status lives in the running ownscale process, so
// this only works where cfg.DataDir is shared with it.
func probeHeadscale(ctx context.Context, client *hscl.Client) probeResult {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return probeResult{status: "unreachable"}
	}
	res := probeResult{status: "running", machines: len(nodes)}
	for _, n := range nodes {
		if n.Online {
			res.online++
		}
	}
	return res
}

// listOrganizations prints every organization recorded in queries to out,
// with the status and machine counts probe reports for its subdomain.
func listOrganizations(ctx context.Context, out io.Writer, queries *db.TxQueries, probe func(ctx context.Context, subdomain string) probeResult) (err error) {
	defer derrors.Wrap(&err, "listOrganizations()")
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return err
	}
	if len(organizations) == 0 {
		_, _ = fmt.Fprintln(out, "no organizations")
		return nil
	}

	_, _ = fmt.Fprintf(out, "%-20s %-6s %-9s %-8s %-12s %-20s\n", "SUBDOMAIN", "USERS", "MACHINES", "ONLINEM", "STATUS", "CREATED")
	for _, org := range organizations {
		ucount, err := queries.CountOrgUsers(ctx, org.ID)
		if err != nil {
			return err
		}

		res := probe(ctx, org.Subdomain)
		machines, online := "-", "-"
		if res.status == "running" {
			machines, online = strconv.Itoa(res.machines), strconv.Itoa(res.online)
		}
		_, _ = fmt.Fprintf(out, "%-20s %-6d %-9s %-8s %-12s %-20s\n",
			org.Subdomain, ucount, machines, online, res.status,
			org.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}
