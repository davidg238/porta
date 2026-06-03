// internal/portacli/monitor.go
package portacli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
	"github.com/spf13/cobra"
)

// telemetryReader is the slice of apiclient.Client that monitor needs, so the
// follow loop can be tested with an in-memory fake.
type telemetryReader interface {
	QueryTelemetryWindow(sel string, since, until int64, kind string, limit int) ([]apiclient.DataRow, error)
	QueryTelemetryAfter(sel string, after int64, kind string, limit int) ([]apiclient.DataRow, error)
}

// toStoreRow adapts an apiclient.DataRow to the store.DataRow telemetry.FormatLine
// expects, so monitor's output is byte-for-byte identical to the db-backed past.
func toStoreRow(r apiclient.DataRow) store.DataRow {
	return store.DataRow{
		TS: r.TS, Seq: r.Seq, Kind: r.Kind, Name: r.Name,
		Value: r.Value, Text: r.Text, ValueType: r.ValueType,
	}
}

// runMonitor is the testable core of `porta monitor`. It prints the node's
// telemetry over the API: first the ts window [now-sinceS, now], then — if
// follow — it polls every pollInterval for rows with id past the highest id
// seen, advancing an exact id cursor (no timestamp-tie boundary case). It
// returns nil on ctx cancellation (Ctrl-C).
func runMonitor(ctx context.Context, out io.Writer, c telemetryReader,
	sel string, sinceS int64, kind string, follow bool,
	now func() int64, pollInterval time.Duration,
) error {
	until := now()
	rows, err := c.QueryTelemetryWindow(sel, until-sinceS, until, kind, 0)
	if err != nil {
		return err
	}
	var cursor int64
	for _, r := range rows {
		fmt.Fprintln(out, telemetry.FormatLine(toStoreRow(r)))
		if r.ID > cursor {
			cursor = r.ID
		}
	}
	if !follow {
		return nil
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				return nil
			}
			return ctx.Err()
		case <-ticker.C:
			rows, err := c.QueryTelemetryAfter(sel, cursor, kind, 0)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintln(out, telemetry.FormatLine(toStoreRow(r)))
				if r.ID > cursor {
					cursor = r.ID
				}
			}
		}
	}
}

func newMonitorCmd() *cobra.Command {
	var device, since, kind string
	var follow bool
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Print a node's telemetry over the API; --follow tails new rows",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceS := int64(3600)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			c := apiclient.New(serverURL())
			return runMonitor(cmd.Context(), cmd.OutOrStdout(), c, device, sinceS, kind, follow, nowSec, 2*time.Second)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 30m, 1h (default 1h)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter to 'log' or 'metric'")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll the server and tail new rows")
	return cmd
}
