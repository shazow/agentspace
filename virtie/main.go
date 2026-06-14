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
	"strings"
	"syscall"

	"github.com/jessevdk/go-flags"
	"github.com/shazow/agentspace/virtie/internal/balloon"
	"github.com/shazow/agentspace/virtie/internal/manager"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type Options struct {
	Manifest string `long:"manifest" value-name:"MANIFEST" description:"Path to the virtie manifest"`
	Verbose  []bool `short:"v" long:"verbose" description:"Show verbose logging."`

	Launch struct {
		Resume              string `long:"resume" choice:"no" choice:"auto" choice:"force" default:"auto" description:"Resume suspended VM instead of launching a fresh one"`
		SSH                 bool   `long:"ssh" description:"Attach an SSH session after launch readiness"`
		AlwaysDeleteSockets bool   `long:"always-delete-sockets" description:"Delete stale socket files without prompting"`

		Args struct {
			RemoteCommand []string `positional-arg-name:"remote-cmd"`
		} `positional-args:"yes"`
	} `command:"launch" description:"Launch a virtiofs + ssh sandbox session" long-description:"Start configured host-side run processes, launch QEMU directly, then optionally attach over ssh."`

	Suspend struct{} `command:"suspend" description:"Suspend a running sandbox session" long-description:"Save QEMU state to disk and exit the launch session."`

	Hotplug struct {
		Detach bool `long:"detach" description:"Detach the hotplug device instead of attaching it"`

		Args struct {
			ID string `positional-arg-name:"id" required:"yes"`
		} `positional-args:"yes"`
	} `command:"hotplug" description:"Attach or detach a predefined hotplug device" long-description:"Attach or detach a device described under manifest [hotplug]."`
}

func runLaunch(options *Options) error {
	if len(options.Launch.Args.RemoteCommand) > 0 && !options.Launch.SSH {
		return fmt.Errorf("remote command arguments require --ssh")
	}

	baseLogger := slog.Default()
	discardLogger := slog.New(slog.DiscardHandler)
	manifestLogger := discardLogger
	manager.SetLogger(discardLogger)
	balloon.SetLogger(discardLogger)
	if len(options.Verbose) > 0 {
		manifestLogger = baseLogger.With("package", "manifest")
		manager.SetLogger(baseLogger.With("package", "manager"))
	}
	if len(options.Verbose) > 1 {
		balloon.SetLogger(baseLogger.With("package", "balloon"))
	}

	manifest, err := loadLaunchManifest(options.Manifest, manifestLogger)
	if err != nil {
		return err
	}

	return manager.LaunchWithOptions(context.Background(), manifest, options.Launch.Args.RemoteCommand, manager.LaunchOptions{
		Resume:              manager.ResumeMode(options.Launch.Resume),
		SSH:                 options.Launch.SSH,
		Verbosity:           len(options.Verbose),
		AlwaysDeleteSockets: options.Launch.AlwaysDeleteSockets,
	})
}

func runSuspend(options *Options) error {
	manifest, err := loadManifest(options.Manifest)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return manager.Suspend(ctx, manifest)
}

func runHotplug(options *Options) error {
	baseLogger := slog.Default()
	discardLogger := slog.New(slog.DiscardHandler)
	manifestLogger := discardLogger
	manager.SetLogger(discardLogger)
	if len(options.Verbose) > 0 {
		manifestLogger = baseLogger.With("package", "manifest")
		manager.SetLogger(baseLogger.With("package", "manager"))
	}

	manifest, err := loadLaunchManifest(options.Manifest, manifestLogger)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return manager.Hotplug(ctx, manifest, options.Hotplug.Args.ID, options.Hotplug.Detach)
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

	return doc.ManifestWithOptions(manifest.ResolveOptions{Logger: logger})
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
	resolvedPath, err := resolveManifestPath(path)
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

func resolveManifestPath(path string) (string, error) {
	if path != "" {
		return filepath.Abs(path)
	}

	var checked []string
	for _, candidate := range []string{"manifest.toml", "manifest.json"} {
		resolved, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		checked = append(checked, resolved)
		if _, err := os.Stat(resolved); err == nil {
			return resolved, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	return "", fmt.Errorf("no manifest path provided and no default manifest found; checked %s", strings.Join(checked, ", "))
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
	if err := run(os.Args[1:]); err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}

		fmt.Fprintln(os.Stderr, err)
		os.Exit(manager.ExitCode(err))
	}
}

func run(args []string) error {
	opts := &Options{}
	parser := newParserForOptions(opts)

	if _, err := parser.ParseArgs(args); err != nil {
		return err
	}

	switch parser.Active.Name {
	case "launch":
		return runLaunch(opts)
	case "suspend":
		return runSuspend(opts)
	case "hotplug":
		return runHotplug(opts)
	default:
		return fmt.Errorf("unknown command %q", parser.Active.Name)
	}
}

func newParser() *flags.Parser {
	return newParserForOptions(&Options{})
}

func newParserForOptions(opts *Options) *flags.Parser {
	return flags.NewParser(opts, flags.Default|flags.PassDoubleDash)
}
