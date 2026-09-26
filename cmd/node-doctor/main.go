package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/stevensiwarski/labfleet/internal/diagnostics"
)

func status() string { return "node-doctor: diagnostics ready" }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(diagnostics.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
