// Command virtie launches the supported agentspace sandbox session.
package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	Resume  string `long:"resume" choice:"no" choice:"auto" choice:"force" default:"auto" description:"Resume suspended VM instead of launching a fresh one"`
	SSH     bool   `long:"ssh" description:"Attach an SSH session after launch readiness"`
	Verbose []bool `short:"v" long:"verbose" description:"Show verbose logging."`

	Args struct {
		RemoteCommand []string `positional-arg-name:"remote-cmd"`
	} `positional-args:"yes"`
}

func (c *launchCommand) Execute(args []string) error {
	if len(c.Args.RemoteCommand) > 0 && !c.SSH {
		return fmt.Errorf("remote command arguments require --ssh")
	}

	manifest, err := loadLaunchManifest(c.Manifest)
	if err != nil {
		return err
	}

	return manager.LaunchWithOptions(context.Background(), manifest, c.Args.RemoteCommand, manager.LaunchOptions{
		Resume:    manager.ResumeMode(c.Resume),
		SSH:       c.SSH,
		Verbosity: len(c.Verbose),
	})
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

func loadLaunchManifest(path string) (*manifest.Manifest, error) {
	manifest, resolvedPath, data, err := loadManifestData(path)
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(manifest.Paths.WorkingDir) {
		return manifest, nil
	}

	workingDir, err := filepath.Abs(manifest.Paths.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve manifest working directory %q: %w", manifest.Paths.WorkingDir, err)
	}
	manifest.Paths.WorkingDir = workingDir

	if err := writeManifestWorkingDir(resolvedPath, data, workingDir); err != nil {
		return nil, err
	}
	return manifest, nil
}

func loadManifest(path string) (*manifest.Manifest, error) {
	manifest, _, _, err := loadManifestData(path)
	return manifest, err
}

func loadManifestData(path string) (*manifest.Manifest, string, []byte, error) {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolve manifest path %q: %w", path, err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, "", nil, fmt.Errorf("open manifest %q: %w", resolvedPath, err)
	}
	manifest, err := manifest.Load(bytes.NewReader(data))
	if err != nil {
		return nil, "", nil, fmt.Errorf("load manifest %q: %w", resolvedPath, err)
	}

	return manifest, resolvedPath, data, nil
}

func writeManifestWorkingDir(path string, data []byte, workingDir string) error {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode manifest %q for update: %w", path, err)
	}

	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return fmt.Errorf("decode manifest %q for update: manifest.paths must be an object", path)
	}
	paths["workingDir"] = workingDir

	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest %q: %w", path, err)
	}
	updated = append(updated, '\n')

	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary manifest %q: %w", path, err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if _, err := temp.Write(updated); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary manifest %q: %w", tempPath, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary manifest %q: %w", tempPath, err)
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return fmt.Errorf("chmod temporary manifest %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace manifest %q: %w", path, err)
	}
	return nil
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
		"Start the configured virtiofsd daemons, launch QEMU directly, then optionally attach over ssh.",
		&launchCommand{},
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if _, err := parser.AddCommand(
		"suspend",
		"Suspend a running sandbox session",
		"Save QEMU state to disk and exit the launch session.",
		&suspendCommand{},
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	return parser
}
