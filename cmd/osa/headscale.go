package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/juanfont/headscale/cmd/headscale/cli"
	"github.com/peterbourgon/ff/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/ownscalenet/ownscale/orgs"
)

const headscaleUsage = "osa headscale SUBDOMAIN [HEADSCALE ARGS...]"

// newHeadscaleCommand builds the "headscale" subcommand, which runs the
// headscale CLI against an organization's headscale instance.
func newHeadscaleCommand(parent *ff.FlagSet) *ff.Command {
	flags := ff.NewFlagSet("headscale").SetParent(parent)
	return &ff.Command{
		Name:      "headscale",
		Usage:     headscaleUsage,
		ShortHelp: "run the headscale CLI against an organization's headscale instance",
		Flags:     flags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) < 1 {
				return errors.New("usage: " + headscaleUsage)
			}
			cfg, _, err := loadConfigAndLogger()
			if err != nil {
				return err
			}
			// The socket is served by the running ownscale process, so this
			// only works where cfg.DataDir is shared with it.
			sock := orgs.SocketPath(cfg.DataDir, args[0])
			if _, err := os.Stat(sock); errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%s not found: the org may not exist or may be sleeping, "+
					"try waking it up by logging in the console "+
					"or connecting a machine on the tailnet", sock)
			}
			// No config file: headscale's CLI reads these via viper.
			if err := os.Setenv("HEADSCALE_UNIX_SOCKET", sock); err != nil {
				return err
			}
			if err := os.Setenv("HEADSCALE_DISABLE_CHECK_UPDATES", "true"); err != nil {
				return err
			}
			log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
			// cli.Execute runs cobra on os.Args and exits the process on error.
			os.Args = append([]string{"headscale"}, args[1:]...)
			cli.Execute()
			return nil
		},
	}
}
