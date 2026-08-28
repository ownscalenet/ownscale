package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/db"
)

// newUserCommand builds the "user" subcommand group (currently just
// "list"), parented under rootFlags so every subcommand also accepts the
// root --config flag.
func newUserCommand(rootFlags *ff.FlagSet, configPath *string) *ff.Command {
	userFlags := ff.NewFlagSet("user").SetParent(rootFlags)
	userCmd := &ff.Command{
		Name:      "user",
		Usage:     "osa user SUBCOMMAND ...",
		ShortHelp: "manage ownscale users",
		Flags:     userFlags,
	}

	userCmd.Subcommands = append(userCmd.Subcommands,
		newUserListCommand(userFlags, configPath),
	)
	return userCmd
}

func newUserListCommand(parent *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("user list").SetParent(parent)
	return &ff.Command{
		Name:      "list",
		Usage:     "osa user list [FLAGS]",
		ShortHelp: "list users and their organizations",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return errors.New("usage: osa user list")
			}
			res, cleanup, err := newOrgResources(configPath, true, false)
			defer cleanup()
			if err != nil {
				return err
			}
			return listUsers(ctx, os.Stdout, res.queries)
		},
	}
}

// listUsers prints every user recorded in queries, along with their
// organization's subdomain (if any) and other relevant account info, to
// out.
func listUsers(ctx context.Context, out io.Writer, queries *db.TxQueries) error {
	users, err := queries.ListUsersWithOrganization(ctx)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	if len(users) == 0 {
		_, _ = fmt.Fprintln(out, "no users")
		return nil
	}

	_, _ = fmt.Fprintf(out, "%-38s %-30s %-15s %-10s %-10s %-12s %-20s %s\n",
		"ID", "EMAIL", "ORGANIZATION", "ROLE", "PROVIDER", "HEADSCALEID", "CREATED", "LAST LOGIN")
	for _, u := range users {
		org := "-"
		if u.OrgSubdomain.Valid {
			org = u.OrgSubdomain.String
		}
		headscaleID := "-"
		if u.HeadscaleUserID.Valid {
			headscaleID = u.HeadscaleUserID.String
		}
		lastLogin := "-"
		if u.LastLoginAt.Valid {
			lastLogin = u.LastLoginAt.Time.Format("2006-01-02 15:04:05")
		}
		_, _ = fmt.Fprintf(out, "%-38s %-30s %-15s %-10s %-10s %-12s %-20s %s\n",
			u.ID, u.Email, org, u.Role, u.Provider, headscaleID,
			u.CreatedAt.Format("2006-01-02 15:04:05"), lastLogin)
	}
	return nil
}
