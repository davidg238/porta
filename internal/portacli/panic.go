// internal/portacli/panic.go
package portacli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/davidg238/porta/internal/apiclient"
	"github.com/davidg238/porta/internal/command"
	"github.com/spf13/cobra"
)

// newPanicCmd is the `porta panic` group: browse and decode node panics.
func newPanicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "panic",
		Short: "Browse and decode node panics",
	}
	cmd.AddCommand(newPanicListCmd(), newPanicShowCmd())
	return cmd
}

// tailReversed keeps the most-recent `limit` rows (input is ascending by time)
// and returns them newest-first. limit <= 0 keeps all rows.
func tailReversed(rows []apiclient.DataRow, limit int) []apiclient.DataRow {
	if limit > 0 && len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	out := make([]apiclient.DataRow, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

// runPanicList is the testable core of `porta panic list`: a newest-first table
// of recent kind:"panic" rows with id, time, and a one-line decoded summary.
func runPanicList(out io.Writer, c telemetryReader, dec panicDecoder, sel string, sinceS int64, limit int, now func() int64) error {
	until := now()
	rows, err := c.QueryTelemetryWindow(sel, until-sinceS, until, "panic", 0)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no panics in window")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTIME\tSUMMARY")
	for _, r := range tailReversed(rows, limit) {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", r.ID, panicTime(r.TS), panicSummary(r, dec))
	}
	return tw.Flush()
}

// runPanicShow is the testable core of `porta panic show`: decode and print one
// kind:"panic" row. id>0 selects by data_log id; id==0 selects the most recent.
func runPanicShow(out io.Writer, c telemetryReader, dec panicDecoder, sel string, sinceS int64, id int64, now func() int64) error {
	until := now()
	rows, err := c.QueryTelemetryWindow(sel, until-sinceS, until, "panic", 0)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no panics in the last window for %s", sel)
	}
	var row apiclient.DataRow
	if id > 0 {
		found := false
		for _, r := range rows {
			if r.ID == id {
				row, found = r, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no panic with id %d in window (try `porta panic list`)", id)
		}
	} else {
		row = rows[len(rows)-1] // rows are ascending; last is most recent
	}
	renderPanic(out, row, dec)
	return nil
}

func newPanicShowCmd() *cobra.Command {
	var device, since string
	var id int64
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Decode and print one panic (default: most recent)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceS := int64(86400)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			c := apiclient.New(serverURL())
			return runPanicShow(cmd.OutOrStdout(), c, newJagDecoder(), device, sinceS, id, nowSec)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 6h, 24h (default 24h)")
	cmd.Flags().Int64Var(&id, "id", 0, "data_log id shown by 'porta panic list' (default: most recent)")
	return cmd
}

func newPanicListCmd() *cobra.Command {
	var device, since string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent panics for a node (newest first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceS := int64(86400)
			if since != "" {
				s, err := command.ParseDurationSeconds(since)
				if err != nil {
					return err
				}
				sinceS = s
			}
			c := apiclient.New(serverURL())
			return runPanicList(cmd.OutOrStdout(), c, newJagDecoder(), device, sinceS, limit, nowSec)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&since, "since", "", "look-back window, e.g. 6h, 24h (default 24h)")
	cmd.Flags().IntVar(&limit, "limit", 20, "max panics to show (most recent)")
	return cmd
}
