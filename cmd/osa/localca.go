package main

import (
	"context"
	"fmt"

	"github.com/peterbourgon/ff/v4"

	"github.com/ownscalenet/ownscale/localca"
)

// Generate a self-signed CA for local development, to have https on a domain
// such as *.test
func newGenLocalCACommand(rootFlags *ff.FlagSet, configPath *string) *ff.Command {
	flags := ff.NewFlagSet("gen-local-ca").SetParent(rootFlags)
	var caddyDataDir string
	flags.StringVar(&caddyDataDir, 0, "caddy-data-dir", "", "path to Caddy's data volume (see docker-compose.yml's caddy-data); if set and a new CA was just generated, wipes Caddy's own cached PKI state under it so it doesn't keep signing certs from a now-orphaned intermediate CA tied to the old root")
	return &ff.Command{
		Name:      "gen-local-ca",
		Usage:     "osa gen-local-ca [FLAGS]",
		ShortHelp: "generate the local development root CA (no-op if it already exists)",
		Flags:     flags,
		Exec: func(_ context.Context, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("usage: osa gen-local-ca")
			}
			cfg, _, err := loadConfigAndLogger(configPath)
			if err != nil {
				return err
			}
			if cfg.LocalCADir == "" {
				return fmt.Errorf("local_ca_dir must be set (e.g. via config.yaml or OWNSCALE_LOCAL_CA_DIR)")
			}
			generated, err := localca.LoadOrGenerate(cfg.LocalCADir)
			if err != nil {
				return err
			}
			if generated && caddyDataDir != "" {
				if err := localca.ResetCaddyState(caddyDataDir); err != nil {
					return err
				}
				fmt.Printf("generated a new local CA at %s; reset Caddy's cached PKI state under %s\n", cfg.LocalCADir, caddyDataDir)
				return nil
			}
			fmt.Printf("local CA ready at %s\n", cfg.LocalCADir)
			return nil
		},
	}
}
