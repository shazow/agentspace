package main

import "testing"

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--socket-path=/tmp/store.sock",
		"--shared-dir=/nix/store",
		"--debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.socketPath != "/tmp/store.sock" || cfg.sharedDir != "/nix/store" || !cfg.debug {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseConfigRequiresSocketAndRoot(t *testing.T) {
	for _, args := range [][]string{
		{"--shared-dir=/nix/store"},
		{"--socket-path=/tmp/store.sock"},
	} {
		if _, err := parseConfig(args); err == nil {
			t.Fatalf("parseConfig(%q) succeeded", args)
		}
	}
}
