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

type manifestOption struct {
	Manifest string `long:"manifest" value-name:"MANIFEST" required:"yes" description:"Path to the virtie manifest"`
}

type launchCommand struct {
	manifestOption

	Args struct {
		RemoteCommand []string `positional-arg-name:"remote-cmd"`
	} `positional-args:"yes"`
}

func (c *launchCommand) Execute(args []string) error {
	manifest, err := loadManifest(c.Manifest)
	if err != nil {
		return err
	}

	return manager.Launch(context.Background(), manifest, c.Args.RemoteCommand)
}

type suspendCommand struct {
	manifestOption
}

func (c *suspendCommand) Execute(args []string) error {
	manifest, err := loadManifest(c.Manifest)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return manager.Suspend(ctx, manifest)
}

type resumeCommand struct {
	manifestOption
}

func (c *resumeCommand) Execute(args []string) error {
	manifest, err := loadManifest(c.Manifest)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return manager.Resume(ctx, manifest)
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
	parser := newParser()

	if _, err := parser.Parse(); err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}

		fmt.Fprintln(os.Stderr, err)
		os.Exit(manager.ExitCode(err))
	}
}

func newParser() *flags.Parser {
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

	if _, err := parser.AddCommand(
		"suspend",
		"Suspend a running sandbox session",
		"Ask the configured launch process to pause QEMU and record advisory suspend state.",
		&suspendCommand{},
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if _, err := parser.AddCommand(
		"resume",
		"Resume a suspended sandbox session",
		"Ask the configured launch process to continue QEMU and remove advisory suspend state.",
		&resumeCommand{},
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	return parser
}
