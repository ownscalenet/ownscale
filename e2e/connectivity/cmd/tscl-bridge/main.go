// The tscl-bridge command exposes ephemeral connectivity nodes over JSON lines.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ownscalenet/ownscale/e2e/connectivity/internal/bridge"
)

func main() {
	log.SetOutput(io.Discard)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	input, err := pollableInput(os.Stdin)
	if err == nil {
		err = bridge.Run(ctx, input, os.Stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "tscl-bridge: exiting due to error: %v\n", err)
		os.Exit(1)
	}
}

func pollableInput(input *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(input.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	// Inherited stdin can be blocking; make Close interrupt an outstanding read.
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "bridge-stdin"), nil
}
