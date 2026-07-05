// Command lucid is the entry point for the lucid binary. All logic
// lives in internal/cli; this file is a thin shim that injects build
// metadata and exits with the resolved code.
package main

import (
	"context"
	"os"

	"github.com/mrz1836/lucid/internal/cli"
)

// Build identification injected via ldflags by goreleaser / magex:
//
//	-X main.version=...   -X main.commit=...   -X main.buildDate=...
//
//nolint:gochecknoglobals // build-time injected metadata; immutable at runtime
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	os.Exit(cli.Execute(context.Background(), cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    buildDate,
	}))
}
