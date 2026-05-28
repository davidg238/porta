// internal/portacli/monitor.go
package portacli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
	"github.com/spf13/cobra"
)

// runMonitor is the testable core of `porta monitor`. It prints the
// data_log rows for (id, sinceS look-back, kind filter), formatted via
// telemetry.FormatLine. If follow=true, it polls every pollInterval until
// ctx is cancelled, advancing the watermark by (last+1) to dedup. The
// boundary-row edge case (rows sharing the poll-tick ts) is accepted as-is
// (see spec §7).
func runMonitor(ctx context.Context, out io.Writer, st *store.Store,
	id string, sinceS int64, kind string, follow bool,
	now func() int64, pollInterval time.Duration,
) error {
	until := now()
	since := until - sinceS
	rows, err := st.QueryData(id, since, until, kind)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Fprintln(out, telemetry.FormatLine(r))
	}
	if !follow {
		return nil
	}
	last := until
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err == context.Canceled {
				return nil
			}
			return err
		case <-ticker.C:
			t := now()
			rows, err := st.QueryData(id, last+1, t, kind)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintln(out, telemetry.FormatLine(r))
			}
			last = t
		}
	}
}

func newMonitorCmd() *cobra.Command {
	var device, since, kind string
	var follow bool
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Print a node's telemetry; --follow tails new rows as wakes deliver them",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			sinceS := int64(3600)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			return runMonitor(cmd.Context(), cmd.OutOrStdout(), st, id, sinceS, kind, follow, nowSec, 2*time.Second)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 30m, 1h (default 1h)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter to 'log' or 'metric'")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll the store and tail new rows")
	return cmd
}
