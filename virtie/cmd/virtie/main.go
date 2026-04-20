package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jessevdk/go-flags"
	"github.com/shazow/agentspace/virtie"
)

type options struct{}

type launchCommand struct {
	Args struct {
		Manifest      string   `positional-arg-name:"manifest" required:"yes"`
		RemoteCommand []string `positional-arg-name:"remote-cmd"`
	} `positional-args:"yes"`
}

func (c *launchCommand) Execute(args []string) error {
	manifest, err := virtie.LoadManifest(c.Args.Manifest)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	manager := virtie.NewManager()
	return manager.Launch(ctx, manifest, c.Args.RemoteCommand)
}

func main() {
	var opts options
	parser := flags.NewParser(&opts, flags.Default|flags.PassDoubleDash)

	if _, err := parser.AddCommand(
		"launch",
		"Launch a virtiofs + ssh sandbox session",
		"Start the configured virtiofsd daemons, launch QEMU directly, then attach over ssh.",
		&launchCommand{},
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if _, err := parser.Parse(); err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}

		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	var cmdErr *virtie.CommandError
	if errors.As(err, &cmdErr) && cmdErr.ExitCode >= 0 {
		return cmdErr.ExitCode
	}

	if errors.Is(err, context.Canceled) {
		return 130
	}

	return 1
}
