package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/stevensiwarski/labfleet/provisioning/internal/bootstrap"
)

func run(args []string) error {
	fs := flag.NewFlagSet("seedctl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	config := fs.String("config", "", "path to local seed configuration JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *config == "" {
		return fmt.Errorf("usage: seedctl --config FILE")
	}
	c, err := bootstrap.LoadConfig(*config)
	if err != nil {
		return err
	}
	return bootstrap.Render(c)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
