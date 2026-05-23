// Command virtie launches the supported agentspace sandbox session.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jessevdk/go-flags"
	"github.com/shazow/agentspace/virtie/internal/balloon"
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

	baseLogger := slog.Default()
	discardLogger := slog.New(slog.DiscardHandler)
	manifestLogger := discardLogger
	manager.SetLogger(discardLogger)
	balloon.SetLogger(discardLogger)
	if len(c.Verbose) > 0 {
		manifestLogger = baseLogger.With("package", "manifest")
		manager.SetLogger(baseLogger.With("package", "manager"))
	}
	if len(c.Verbose) > 1 {
		balloon.SetLogger(baseLogger.With("package", "balloon"))
	}

	manifest, err := loadLaunchManifest(c.Manifest, manifestLogger)
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

func loadLaunchManifest(path string, logger *slog.Logger) (*manifest.Manifest, error) {
	doc, resolvedPath, data, err := loadManifestDocumentData(path)
	if err != nil {
		return nil, err
	}
	workingDir := doc.WorkingDir
	if workingDir == "" {
		workingDir = "."
	}
	if !filepath.IsAbs(workingDir) {
		resolvedWorkingDir, err := filepath.Abs(workingDir)
		if err != nil {
			return nil, fmt.Errorf("resolve manifest working directory %q: %w", workingDir, err)
		}
		doc.WorkingDir = resolvedWorkingDir
		if err := writeManifestWorkingDir(resolvedPath, data, resolvedWorkingDir); err != nil {
			return nil, err
		}
	}

	return doc.ManifestWithOptions(manifest.LowerOptions{Logger: logger})
}

func loadManifest(path string) (*manifest.Manifest, error) {
	manifest, _, _, err := loadManifestData(path)
	return manifest, err
}

func loadManifestData(path string) (*manifest.Manifest, string, []byte, error) {
	doc, resolvedPath, data, err := loadManifestDocumentData(path)
	if err != nil {
		return nil, "", nil, err
	}
	manifest, err := doc.Manifest()
	if err != nil {
		return nil, "", nil, fmt.Errorf("load manifest %q: %w", resolvedPath, err)
	}
	return manifest, resolvedPath, data, nil
}

func loadManifestDocumentData(path string) (manifest.Document, string, []byte, error) {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return manifest.Document{}, "", nil, fmt.Errorf("resolve manifest path %q: %w", path, err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return manifest.Document{}, "", nil, fmt.Errorf("open manifest %q: %w", resolvedPath, err)
	}
	doc, err := manifest.DecodeDocumentBytes(data, resolvedPath)
	if err != nil {
		return manifest.Document{}, "", nil, fmt.Errorf("load manifest %q: %w", resolvedPath, err)
	}

	return doc, resolvedPath, data, nil
}

func writeManifestWorkingDir(path string, data []byte, workingDir string) error {
	updated, err := manifest.UpdateWorkingDirBytes(data, path, workingDir)
	if err != nil {
		return fmt.Errorf("update manifest %q working dir: %w", path, err)
	}

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
		"Start configured host-side run processes, launch QEMU directly, then optionally attach over ssh.",
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
