package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/derrors"
	"github.com/ownscalenet/ownscale/hscl"
	"github.com/ownscalenet/ownscale/orgs"
)

const devboxKeyTTL = 10 * time.Minute

// newDevboxKeyCommand builds the "devbox-key" subcommand, which `make
// devbox` uses to join a throwaway tailscale node to an organization.
func newDevboxKeyCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("devbox-key").SetParent(parent)
	return &ff.Command{
		Name:      "devbox-key",
		Usage:     "osa devbox-key SUBDOMAIN",
		ShortHelp: "print a short-lived ephemeral pre-auth key for an organization",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: osa devbox-key SUBDOMAIN")
			}
			cfg, _, err := loadConfigAndLogger()
			if err != nil {
				return err
			}
			// The socket is served by the running ownscale process, so this
			// only works where cfg.DataDir is shared with it.
			return devboxKey(ctx, os.Stdout, hscl.NewUnix(orgs.SocketPath(cfg.DataDir, args[0])))
		},
	}
}

// devboxKey writes a new ephemeral pre-auth key to out, owned by the
// organization's first headscale user (created if there are none).
func devboxKey(ctx context.Context, out io.Writer, c *hscl.Client) (err error) {
	defer derrors.Wrap(&err, "devboxKey()")

	users, err := c.ListUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		u, err := c.CreateUser(ctx, hscl.CreateUserParams{Name: "devbox"})
		if err != nil {
			return err
		}
		users = append(users, u)
	}
	key, err := c.CreatePreAuthKey(ctx, hscl.CreatePreAuthKeyParams{
		UserID:     users[0].ID,
		Ephemeral:  true,
		Expiration: time.Now().Add(devboxKeyTTL),
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, key.Key)
	return err
}
