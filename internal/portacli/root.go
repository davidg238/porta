// Package portacli is the porta gateway's cobra command tree.
package portacli

import (
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

// Execute runs the porta CLI.
func Execute() error { return NewRootCmd().Execute() }
