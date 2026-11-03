// Command osa is the operator CLI for managing ownscale organizations and
// their databases, independent of the ownscale HTTP server binary
// (cmd/ownscale).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
)

func main() {
	rootFlags := ff.NewFlagSet("osa")

	rootCmd := &ff.Command{
		Name:      "osa",
		Usage:     "osa [FLAGS] SUBCOMMAND ...",
		ShortHelp: "manage ownscale organizations",
		Flags:     rootFlags,
	}
	rootCmd.Subcommands = append(rootCmd.Subcommands,
		newOrganizationCommand(rootFlags),
		newUserCommand(rootFlags),
		newMigrateCommand(rootFlags),
		newGenLocalCACommand(rootFlags),
		newRestoreCommand(rootFlags),
		newVerifyCommand(rootFlags),
		newDevboxKeyCommand(rootFlags),
		newHeadscaleCommand(rootFlags),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := rootCmd.ParseAndRun(ctx, os.Args[1:])
	if errors.Is(err, ff.ErrHelp) {
		// Asking for help is not a failure.
		fmt.Fprintf(os.Stdout, "%s\n", ffhelp.Command(rootCmd))
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", ffhelp.Command(rootCmd))
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
