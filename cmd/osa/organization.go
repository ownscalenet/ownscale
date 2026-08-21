package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/internal/config"
	"github.com/ownscalenet/ownscale/internal/db"
	"github.com/ownscalenet/ownscale/internal/db/sqlcgen"
	"github.com/ownscalenet/ownscale/internal/dockerctl"
	"github.com/ownscalenet/ownscale/internal/logging"
	"github.com/ownscalenet/ownscale/internal/orgs"
)

// loadConfigAndLogger reads the config file at *configPath (or the
// default path) and builds a logger from its Log settings. It's called
// at the start of every organization subcommand's Exec, since each
// subcommand runs as its own short-lived process invocation.
func loadConfigAndLogger(configPath *string) (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load(*configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	logger, err := logging.New(cfg.Log)
	if err != nil {
		return nil, nil, fmt.Errorf("build logger: %w", err)
	}
	return cfg, logger, nil
}

// baseDomain returns cfg.BaseDomain, or an error if it isn't set: it's
// required by every organization subcommand that provisions or reports
// on a subdomain.
func baseDomain(cfg *config.Config) (string, error) {
	if cfg.BaseDomain == "" {
		return "", errors.New("base_domain must be set (e.g. via config.yaml or OWNSCALE_BASE_DOMAIN=example.com)")
	}
	return cfg.BaseDomain, nil
}

// orgResources bundles the dependencies shared by organization subcommands:
// the config and logger, and (depending on what a subcommand needs) an open
// SQLite connection/query handle and/or a Docker controller. Business-logic
// functions (createOrganization, deleteOrganization, etc.) take these
// already-open dependencies directly rather than opening them itself, which
// both removes the setup duplication across subcommands and lets those
// functions be exercised in tests against already-constructed (real DB,
// fake Docker) dependencies.
type orgResources struct {
	cfg     *config.Config
	logger  *slog.Logger
	pool    *sql.DB
	queries *sqlcgen.Queries
	ctl     *dockerctl.Ctl
}

// newOrgResources loads the config and logger, then optionally opens the
// SQLite database (withDB) and/or the Docker controller (withDocker),
// matching what each organization subcommand actually needs (e.g. "stop"
// never touches the database). The returned cleanup func closes whichever
// of those were actually opened, in reverse order; it's always non-nil and
// safe to defer, even if newOrgResources returns an error.
func newOrgResources(configPath *string, withDB, withDocker bool) (_ *orgResources, cleanup func(), err error) {
	var closers []func() error
	cleanup = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]() //nolint:errcheck // best-effort close on process exit
		}
	}

	cfg, logger, err := loadConfigAndLogger(configPath)
	if err != nil {
		return nil, cleanup, err
	}
	res := &orgResources{cfg: cfg, logger: logger}

	if withDB {
		pool, err := db.Connect(cfg.Database())
		if err != nil {
			return nil, cleanup, fmt.Errorf("db connect: %w", err)
		}
		closers = append(closers, pool.Close)
		res.pool = pool
		res.queries = sqlcgen.New(db.NewLoggingDBTX(pool, logger))
	}

	if withDocker {
		ctl, err := dockerctl.New(cfg.DataDir)
		if err != nil {
			return nil, cleanup, fmt.Errorf("docker init: %w", err)
		}
		closers = append(closers, ctl.Close)
		res.ctl = ctl
	}

	return res, cleanup, nil
}

// newOrganizationCommand builds the "organization" subcommand group
// (create/delete/list/start/stop), parented under rootFlags so every
// subcommand also accepts the root --config flag.
func newOrganizationCommand(rootFlags *ff.FlagSet, configPath *string) *ff.Command {
	organizationFlags := ff.NewFlagSet("organization").SetParent(rootFlags)
	organizationCmd := &ff.Command{
		Name:      "organization",
		Usage:     "osa organization SUBCOMMAND ...",
		ShortHelp: "manage organizations (headscale/headplane containers)",
		Flags:     organizationFlags,
	}

	organizationCmd.Subcommands = append(organizationCmd.Subcommands,
		newOrganizationCreateCommand(organizationFlags, configPath),
		newOrganizationDeleteCommand(organizationFlags, configPath),
		newOrganizationListCommand(organizationFlags, configPath),
		newOrganizationStartCommand(organizationFlags, configPath),
		newOrganizationStopCommand(organizationFlags, configPath),
	)
	return organizationCmd
}

func newOrganizationCreateCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("organization create").SetParent(parent)
	return &ff.Command{
		Name:      "create",
		Usage:     "osa organization create [FLAGS] <subdomain>",
		ShortHelp: "provision a new organization",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: osa organization create <subdomain>")
			}
			res, cleanup, err := newOrgResources(configPath, true, true)
			defer cleanup()
			if err != nil {
				return err
			}
			domain, err := baseDomain(res.cfg)
			if err != nil {
				return err
			}
			svc := orgs.NewService(res.pool, res.ctl, domain, res.logger)
			return createOrganization(ctx, os.Stdout, svc, args[0])
		},
	}
}

func newOrganizationDeleteCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("organization delete").SetParent(parent)
	return &ff.Command{
		Name:      "delete",
		Usage:     "osa organization delete [FLAGS] <subdomain>",
		ShortHelp: "remove an organization and its container",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: osa organization delete <subdomain>")
			}
			res, cleanup, err := newOrgResources(configPath, true, true)
			defer cleanup()
			if err != nil {
				return err
			}
			return deleteOrganization(ctx, os.Stdout, res.queries, res.ctl, args[0])
		},
	}
}

func newOrganizationListCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("organization list").SetParent(parent)
	return &ff.Command{
		Name:      "list",
		Usage:     "osa organization list [FLAGS]",
		ShortHelp: "list organizations and their container state",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa organization list")
			}
			res, cleanup, err := newOrgResources(configPath, true, true)
			defer cleanup()
			if err != nil {
				return err
			}
			return listOrganizations(ctx, os.Stdout, res.queries, res.ctl)
		},
	}
}

func newOrganizationStartCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("organization start").SetParent(parent)
	return &ff.Command{
		Name:      "start",
		Usage:     "osa organization start [FLAGS]",
		ShortHelp: "start every organization's headscale/headplane containers",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa organization start")
			}
			res, cleanup, err := newOrgResources(configPath, true, true)
			defer cleanup()
			if err != nil {
				return err
			}
			return startOrganizations(ctx, os.Stdout, res.logger, res.queries, res.ctl)
		},
	}
}

func newOrganizationStopCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("organization stop").SetParent(parent)
	timeout := flags.DurationLong("timeout", 10*time.Second, "graceful stop timeout")
	return &ff.Command{
		Name:      "stop",
		Usage:     "osa organization stop [FLAGS]",
		ShortHelp: "gracefully stop every organization's headscale/headplane containers",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa organization stop [--timeout <duration>]")
			}
			res, cleanup, err := newOrgResources(configPath, false, true)
			defer cleanup()
			if err != nil {
				return err
			}
			return stopOrganizations(ctx, os.Stdout, res.ctl, *timeout)
		},
	}
}

// createOrganization validates subdomain and provisions it as a new
// organization (DB record + headscale container) under svc's base domain,
// writing a confirmation to out.
func createOrganization(ctx context.Context, out io.Writer, svc *orgs.Service, subdomain string) error {
	if err := dockerctl.ValidateSubdomain(subdomain); err != nil {
		return fmt.Errorf("validate subdomain: %w", err)
	}

	if _, err := svc.Create(ctx, subdomain); err != nil {
		return fmt.Errorf("create organization: %w", err)
	}

	domain := svc.BaseDomain()
	_, _ = fmt.Fprintf(out, "created organization %s.%s (container %s)\n", subdomain, domain, dockerctl.HSContainerName(subdomain))
	_, _ = fmt.Fprintf(out, "headscale will be reachable at https://%s.%s once Caddy is running\n", subdomain, domain)
	return nil
}

// deleteOrganization looks up subdomain, removes its container via ctl,
// then deletes its DB record, writing a confirmation to out.
func deleteOrganization(ctx context.Context, out io.Writer, queries *sqlcgen.Queries, ctl *dockerctl.Ctl, subdomain string) error {
	org, err := queries.GetOrganizationBySubdomain(ctx, subdomain)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no organization with subdomain %q", subdomain)
	}
	if err != nil {
		return fmt.Errorf("get organization: %w", err)
	}

	if err := ctl.RemoveOrganization(ctx, subdomain); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	if err := queries.DeleteOrganization(ctx, org.ID); err != nil {
		return fmt.Errorf("delete organization record: %w", err)
	}
	_, _ = fmt.Fprintf(out, "deleted organization %s\n", subdomain)
	return nil
}

// listOrganizations prints every organization recorded in queries along
// with its current container state (as reported by ctl) to out.
func listOrganizations(ctx context.Context, out io.Writer, queries *sqlcgen.Queries, ctl *dockerctl.Ctl) error {
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}
	if len(organizations) == 0 {
		_, _ = fmt.Fprintln(out, "no organizations")
		return nil
	}

	_, _ = fmt.Fprintf(out, "%-20s %-12s %-20s %s\n", "SUBDOMAIN", "STATE", "CREATED", "CONTAINER")
	for _, org := range organizations {
		state, err := ctl.ContainerState(ctx, org.Subdomain)
		if err != nil {
			state = "error: " + err.Error()
		}
		_, _ = fmt.Fprintf(out, "%-20s %-12s %-20s %s\n",
			org.Subdomain, state,
			org.CreatedAt.Format("2006-01-02 15:04:05"),
			dockerctl.HSContainerName(org.Subdomain))
	}
	return nil
}

// startOrganizations starts the (pre-existing) headscale/headplane
// containers for every organization recorded in queries. It's the
// counterpart to stopOrganizations, for bringing everything back up after
// a deliberate stop (or a host reboot where the containers' restart
// policy didn't already handle it). It doesn't create or provision
// anything new; organizations that no longer have containers (e.g. removed
// out of band) are reported as errors but don't stop the rest from starting.
func startOrganizations(ctx context.Context, out io.Writer, logger *slog.Logger, queries *sqlcgen.Queries, ctl *dockerctl.Ctl) error {
	organizations, err := queries.ListOrganizations(ctx)
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}
	if len(organizations) == 0 {
		_, _ = fmt.Fprintln(out, "no organizations")
		return nil
	}

	var failed int
	for _, org := range organizations {
		if err := ctl.StartOrganization(ctx, org.Subdomain); err != nil {
			logger.Error("start organization", "subdomain", org.Subdomain, "error", err)
			failed++
			continue
		}
		_, _ = fmt.Fprintf(out, "started organization %s\n", org.Subdomain)
	}
	if failed > 0 {
		return fmt.Errorf("failed to start %d/%d organizations", failed, len(organizations))
	}
	return nil
}

// stopOrganizations gracefully stops every headscale/headplane container
// across all organizations via ctl. It's a deliberate, operator-triggered
// maintenance action (e.g. before a host reboot or `docker compose down`)
// and is not run automatically on ownscale process shutdown, since these
// containers are meant to keep running across ownscale restarts/deploys.
func stopOrganizations(ctx context.Context, out io.Writer, ctl *dockerctl.Ctl, timeout time.Duration) error {
	if err := ctl.StopAll(ctx, timeout); err != nil {
		return fmt.Errorf("stop organizations: %w", err)
	}
	_, _ = fmt.Fprintln(out, "stopped all organization containers")
	return nil
}
