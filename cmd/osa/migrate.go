package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/peterbourgon/ff/v4"
	"github.com/pressly/goose/v3"

	"github.com/ownscalenet/ownscale/internal/db"
)

// newMigrateCommand builds the "migrate" subcommand group (up/down/status),
// which applies schema migrations (embedded from internal/db/migrations)
// against the sqlite database configured in config.yaml, using goose as a
// library. It mirrors what used to be the standalone cmd/migrate binary.
func newMigrateCommand(rootFlags *ff.FlagSet, configPath *string) *ff.Command {
	migrateFlags := ff.NewFlagSet("migrate").SetParent(rootFlags)
	migrateCmd := &ff.Command{
		Name:      "migrate",
		Usage:     "osa migrate SUBCOMMAND ...",
		ShortHelp: "run schema migrations",
		Flags:     migrateFlags,
	}

	migrateCmd.Subcommands = append(migrateCmd.Subcommands,
		newMigrateUpCommand(migrateFlags, configPath),
		newMigrateDownCommand(migrateFlags, configPath),
		newMigrateStatusCommand(migrateFlags, configPath),
	)
	return migrateCmd
}

// newGooseProvider loads config, builds a logger, connects to the DB, and
// constructs a goose.Provider over the embedded migrations. It's called at
// the start of every migrate subcommand's Exec.
func newGooseProvider(configPath *string) (*goose.Provider, *slog.Logger, error) {
	cfg, logger, err := loadConfigAndLogger(configPath)
	if err != nil {
		return nil, nil, err
	}

	pool, err := db.Connect(cfg.Database())
	if err != nil {
		return nil, nil, fmt.Errorf("db connect: %w", err)
	}

	migrationsFS, err := fs.Sub(db.MigrationsFS, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("migrations fs: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, migrationsFS)
	if err != nil {
		return nil, nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, logger, nil
}

func newMigrateUpCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("migrate up").SetParent(parent)
	return &ff.Command{
		Name:      "up",
		Usage:     "osa migrate up [FLAGS]",
		ShortHelp: "apply the next pending migration",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: osa migrate up")
			}
			provider, _, err := newGooseProvider(configPath)
			if err != nil {
				return err
			}
			return migrateUp(ctx, provider)
		},
	}
}

func newMigrateDownCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("migrate down").SetParent(parent)
	return &ff.Command{
		Name:      "down",
		Usage:     "osa migrate down [FLAGS]",
		ShortHelp: "revert the last applied migration",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: osa migrate down")
			}
			provider, _, err := newGooseProvider(configPath)
			if err != nil {
				return err
			}
			return migrateDown(ctx, provider)
		},
	}
}

func newMigrateStatusCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("migrate status").SetParent(parent)
	return &ff.Command{
		Name:      "status",
		Usage:     "osa migrate status [FLAGS]",
		ShortHelp: "show the status of all migrations",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: osa migrate status")
			}
			provider, _, err := newGooseProvider(configPath)
			if err != nil {
				return err
			}
			return migrateStatus(ctx, provider)
		},
	}
}

// migrateUp applies exactly one pending migration.
func migrateUp(ctx context.Context, provider *goose.Provider) error {
	result, err := provider.UpByOne(ctx)
	if err != nil {
		if err == goose.ErrNoNextVersion {
			fmt.Println("no pending migrations")
			return nil
		}
		return fmt.Errorf("up: %w", err)
	}
	fmt.Printf("applied %s (%s)\n", result.Source.Path, result.Duration)
	return nil
}

// migrateDown reverts exactly one applied migration.
func migrateDown(ctx context.Context, provider *goose.Provider) error {
	result, err := provider.Down(ctx)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}
	fmt.Printf("reverted %s (%s)\n", result.Source.Path, result.Duration)
	return nil
}

// migrateStatus prints the state of every known migration.
func migrateStatus(ctx context.Context, provider *goose.Provider) error {
	statuses, err := provider.Status(ctx)
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	for _, s := range statuses {
		state := string(s.State)
		if s.State == goose.StateApplied {
			fmt.Printf("%-20s %-10s applied at %s\n", s.Source.Path, state, s.AppliedAt)
		} else {
			fmt.Printf("%-20s %-10s\n", s.Source.Path, state)
		}
	}
	return nil
}
