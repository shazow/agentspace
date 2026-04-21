// Command virtie launches the supported agentspace sandbox session.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jessevdk/go-flags"
	"github.com/shazow/agentspace/virtie/internal/manager"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type options struct{}

type launchCommand struct {
	Args struct {
		Manifest      string   `positional-arg-name:"manifest" required:"yes"`
		RemoteCommand []string `positional-arg-name:"remote-cmd"`
	} `positional-args:"yes"`
}

func (c *launchCommand) Execute(args []string) error {
	manifest, err := loadManifest(c.Args.Manifest)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return manager.Launch(ctx, manifest, c.Args.RemoteCommand)
}

func loadManifest(path string) (*manifest.Manifest, error) {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve manifest path %q: %w", path, err)
	}

	file, err := os.Open(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("open manifest %q: %w", resolvedPath, err)
	}
	defer file.Close()

	manifest, err := manifest.Load(file)
	if err != nil {
		return nil, fmt.Errorf("load manifest %q: %w", resolvedPath, err)
	}

	return manifest, nil
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
		os.Exit(manager.ExitCode(err))
	}
}
