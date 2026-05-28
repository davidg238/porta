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
)

func nowSec() int64 { return time.Now().Unix() }

func openStore() (*store.Store, error) { return store.Open(dbPath) }

// NewRootCmd builds the porta command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "porta",
		Short: "porta — northbound gateway for nodus-style nodes",
	}
	root.PersistentFlags().StringVar(&dbPath, "db", "porta.db", "SQLite database path")
	root.AddCommand(
		newServeCmd(),
		newScanCmd(),
		newPingCmd(),
		newDeviceCmd(),
		newContainerCmd(),
		newLogCmd(),
		newMonitorCmd(),
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
