package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/markbates/goth/gothic"
	"github.com/peterbourgon/ff/v4"
	"github.com/rs/xid"

	"github.com/ownscalenet/ownscale/auth"
	"github.com/ownscalenet/ownscale/db"
	"github.com/ownscalenet/ownscale/db/sqlcgen"
)

// testAuthProvider is the synthetic goth provider used by "osa user login"
// to identify users it creates.
const testAuthProvider = "test"

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
		newUserLoginCommand(userFlags),
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
			res, cleanup, err := newOrgResources(true, false)
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
			res, cleanup, err := newOrgResources(true, false)
			defer cleanup()
			if err != nil {
				return err
			}
			return deleteUser(ctx, os.Stdout, res.queries, args[0])
		},
	}
}

// deleteUser looks up userID, deletes the corresponding user record, and
// writes a confirmation to out. It does not touch the headscale user.
func deleteUser(ctx context.Context, out io.Writer, queries *db.TxQueries, userID string) error {
	user, err := queries.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no user with id %q", userID)
	}
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if err := queries.DeleteUser(ctx, user.ID); err != nil {
		return fmt.Errorf("delete user record: %w", err)
	}
	_, _ = fmt.Fprintf(out, "deleted user %s (%s)\n", user.ID, user.Email)
	return nil
}

func newUserLoginCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("user login").SetParent(parent)
	name := flags.StringLong("name", "", "display name for a newly created user (defaults to the email)")
	org := flags.StringLong("org", "", "attach the user to this already-active organization's subdomain (only if they don't already belong to one)")
	role := flags.StringLong("role", "owner", "role to assign when --org is given")
	return &ff.Command{
		Name:      "login",
		Usage:     "osa user login [FLAGS] <email>",
		ShortHelp: "for loca/integration only: mint a valid session cookie for a user without going through OAuth ",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: osa user login [--org <subdomain>] [--role <role>] [--name <name>] <email>")
			}
			res, cleanup, err := newOrgResources(true, false)
			defer cleanup()
			if err != nil {
				return err
			}
			auth.Setup(res.cfg.Auth.SessionSecret)
			return loginUser(ctx, os.Stdout, res.queries, args[0], *name, *org, *role)
		},
	}
}

// loginUser finds or creates a user identified by the synthetic
// testAuthProvider/email identity, optionally attaches them to an
// already-active organization then mints a real session cookie for that user.
func loginUser(ctx context.Context, out io.Writer, queries *db.TxQueries, email, name, orgSubdomain, role string) error {
	if name == "" {
		name = email
	}

	user, err := queries.GetUserByProvider(ctx, sqlcgen.GetUserByProviderParams{
		Provider:       testAuthProvider,
		ProviderUserID: email,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		user, err = queries.CreateUser(ctx, sqlcgen.CreateUserParams{
			ID:             xid.New().String(),
			Provider:       testAuthProvider,
			ProviderUserID: email,
			Email:          email,
			Name:           name,
			EmailVerified:  1,
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
	case err != nil:
		return fmt.Errorf("get user: %w", err)
	}

	if orgSubdomain != "" && !user.OrganizationID.Valid {
		org, err := queries.GetOrganizationBySubdomain(ctx, orgSubdomain)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no organization with subdomain %q", orgSubdomain)
		}
		if err != nil {
			return fmt.Errorf("get organization: %w", err)
		}
		if org.Status != "active" {
			return fmt.Errorf("organization %q is not active yet (status %q)", orgSubdomain, org.Status)
		}
		if err := queries.SetUserOrganizationAndRole(ctx, sqlcgen.SetUserOrganizationAndRoleParams{
			OrganizationID: sql.NullString{String: org.ID, Valid: true},
			Role:           role,
			ID:             user.ID,
		}); err != nil {
			return fmt.Errorf("assign organization: %w", err)
		}
		user.OrganizationID = sql.NullString{String: org.ID, Valid: true}
	}

	if err := queries.TouchUserLastLogin(ctx, user.ID); err != nil {
		return fmt.Errorf("touch last login: %w", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	if err := auth.StoreUserSession(rec, req, user.ID); err != nil {
		return fmt.Errorf("mint session: %w", err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == gothic.SessionName {
			_, _ = fmt.Fprintf(out, "%s=%s\n", c.Name, c.Value)
			return nil
		}
	}
	return fmt.Errorf("mint session: %q cookie not found", gothic.SessionName)
}
