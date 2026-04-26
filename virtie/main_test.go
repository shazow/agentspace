package main

import (
	"strings"
	"testing"
)

func TestParserRequiresManifestFlag(t *testing.T) {
	tests := [][]string{
		{"launch"},
		{"launch", "/tmp/manifest.json"},
		{"suspend"},
		{"resume"},
	}

	for _, args := range tests {
		_, err := newParser().ParseArgs(args)
		if err == nil {
			t.Fatalf("ParseArgs(%v) succeeded, expected missing manifest error", args)
		}
		if !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("ParseArgs(%v) error %q does not mention manifest", args, err)
		}
	}
}
