// Copyright (c) 2026 Ekorau LLC

// Package portacli is the porta gateway's cobra command tree.
package portacli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath string

	// Build identity, injected at link time via -ldflags -X (see
	// deploy/build-deb.sh). Defaults make `go run`/tests report a dev build.
	version = "dev"
	commit  = ""
)

func nowSec() int64 { return time.Now().Unix() }

// versionString is what `porta --version` prints and what the status surface
// reports: the version, with the short commit appended when linked in.
func versionString() string {
	if commit != "" {
		return version + " (" + commit + ")"
	}
	return version
}

func openStore() (*store.Store, error) { return store.Open(dbPath) }

// NewRootCmd builds the porta command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "porta",
		Short:   "porta — northbound gateway for nodus-style nodes",
		Version: versionString(),
	}
	root.PersistentFlags().StringVar(&dbPath, "db", "porta.db", "SQLite database path")
	root.PersistentFlags().StringVar(&serverFlag, "server", "",
		"porta server base URL for write commands (default $PORTA_SERVER or http://localhost:6970)")
	root.AddCommand(
		newServeCmd(),
		newScanCmd(),
		newPingCmd(),
		newDeviceCmd(),
		newContainerCmd(),
		newLogCmd(),
		newMonitorCmd(),
		newDebugCmd(),
		newProfileCmd(),
	)
	return root
}

// Execute runs the porta CLI. The root context is cancelled on
// SIGINT/SIGTERM so long-running subcommands (serve, monitor --follow)
// can exit cleanly. Closes the gap from porta/porta#2 — until this
// change, runMonitor's --follow cancel path was unreachable in
// production because cmd.Context() was context.Background().
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewRootCmd().ExecuteContext(ctx)
}
