// Command osa is the operator CLI for managing ownscale organizations
// (headscale/headplane containers), independent of the ownscale HTTP
// server binary (cmd/ownscale).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	"github.com/ownscalenet/ownscale/config"
)

func main() {
	rootFlags := ff.NewFlagSet("osa")
	configPath := rootFlags.StringLong("config", "", "path to config file (default: "+config.DefaultPath+")")

	rootCmd := &ff.Command{
		Name:      "osa",
		Usage:     "osa [FLAGS] SUBCOMMAND ...",
		ShortHelp: "manage ownscale organizations",
		Flags:     rootFlags,
	}
	rootCmd.Subcommands = append(rootCmd.Subcommands,
		newOrganizationCommand(rootFlags, configPath),
		newMigrateCommand(rootFlags, configPath),
		newGenLocalCACommand(rootFlags, configPath),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ParseAndRun(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ffhelp.Command(rootCmd))
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
