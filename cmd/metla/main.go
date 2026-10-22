package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/nklmilojevic/metla/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := cli.Execute(ctx, version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "metla:", err)
		}
		os.Exit(cli.Code(err))
	}
}
