package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/virtiofs"
	storefs "github.com/shazow/agentspace/go-virtiofsd"
)

type config struct {
	socketPath string
	sharedDir  string
	debug      bool
}

func parseConfig(args []string) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("agentspace-storefs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.socketPath, "socket-path", "", "vhost-user Unix socket path")
	flags.StringVar(&cfg.sharedDir, "shared-dir", "", "host directory to expose read-only")
	flags.BoolVar(&cfg.debug, "debug", false, "enable FUSE request logging")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if cfg.socketPath == "" {
		return config{}, errors.New("--socket-path is required")
	}
	if cfg.sharedDir == "" {
		return config{}, errors.New("--shared-dir is required")
	}
	return cfg, nil
}

func run(args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	root, err := storefs.NewRoot(cfg.sharedDir)
	if err != nil {
		return fmt.Errorf("shared directory: %w", err)
	}

	opts := &fs.Options{}
	opts.Debug = cfg.debug
	opts.MountOptions.Debug = cfg.debug
	rawFS := fs.NewNodeFS(root, opts)
	virtiofs.ServeFS(cfg.socketPath, rawFS, &opts.MountOptions)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
