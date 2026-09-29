package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
)

// newUserCommand builds the "user" subcommand group (currently just
// "list"), parented under rootFlags.
func newUserCommand(rootFlags *ff.FlagSet) *ff.Command {
	userFlags := ff.NewFlagSet("user").SetParent(rootFlags)
	userCmd := &ff.Command{
		Name:      "user",
		Usage:     "osa user SUBCOMMAND ...",
		ShortHelp: "manage ownscale users",
		Flags:     userFlags,
	}

	userCmd.Subcommands = append(userCmd.Subcommands,
		newUserListCommand(userFlags),
		newUserDeleteCommand(userFlags),
	)
	return userCmd
}

func newUserListCommand(parent *ff.FlagSet) *ff.Command {
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
			res, cleanup, err := newOrgResources()
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

func newUserDeleteCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("user delete").SetParent(parent)
	return &ff.Command{
		Name:      "delete",
		Usage:     "osa user delete [FLAGS] <user_id>",
		ShortHelp: "delete a user",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: osa user delete <user_id>")
			}
			res, cleanup, err := newOrgResources()
			defer cleanup()
			if err != nil {
				return err
			}
			// The sockets are served by the running ownscale process, so this
			// only works where cfg.DataDir is shared with it.
			clientFor := func(subdomain string) *hscl.Client {
				return hscl.NewUnix(orgs.SocketPath(res.cfg.DataDir, subdomain))
			}
			return deleteUser(ctx, os.Stdout, res.queries, clientFor, args[0])
		},
	}
}

// deleteUser looks up userID, deletes its linked headscale user and that
// user's machines through clientFor(organization subdomain), then deletes
// the user record and writes a confirmation to out.
// TODO: transaction
func deleteUser(ctx context.Context, out io.Writer, queries *db.TxQueries, clientFor func(subdomain string) *hscl.Client, userID string) error {
	user, err := queries.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no user with id %q", userID)
	}
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if user.HeadscaleUserID.Valid {
		org, err := queries.GetOrganization(ctx, user.OrganizationID)
		if err != nil {
			return fmt.Errorf("get organization: %w", err)
		}
		if err := clientFor(org.Subdomain).DeleteUserAndNodes(ctx, user.HeadscaleUserID.String); err != nil {
			return fmt.Errorf("delete headscale user: %w", err)
		}
	}

	if err := queries.DeleteUser(ctx, user.ID); err != nil {
		return fmt.Errorf("delete user record: %w", err)
	}
	_, _ = fmt.Fprintf(out, "deleted user %s (%s)\n", user.ID, user.Email)
	return nil
}
