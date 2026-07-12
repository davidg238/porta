// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/spf13/cobra"
)

func runProfileStart(out io.Writer, c *apiclient.Client, sel, app string, durationS int64, continuous bool, label string) error {
	cid, node, err := c.ProfileStart(sel, app, durationS, continuous, label)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: profile start %s (command #%d)\n", node, app, cid)
	return nil
}

func runProfileStop(out io.Writer, c *apiclient.Client, sel, app string) error {
	cid, node, err := c.ProfileStop(sel, app)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: profile stop %s (command #%d)\n", node, app, cid)
	return nil
}

func runProfilePoll(out io.Writer, c *apiclient.Client, sel string, after int64) error {
	list, err := c.ProfileList(sel, after)
	if err != nil {
		return err
	}
	if s := list.Session; s != nil {
		label := s.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(out, "session: %s (%s) — %s\n", s.App, label, s.StateLabel)
	}
	for _, r := range list.Results {
		label := r.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(out, "#%d  %s  %s  %d bytes\n", r.Seq, r.App, label, r.ByteLen)
	}
	return nil
}

func runProfileGet(out io.Writer, c *apiclient.Client, sel string, seq int64, outFile string) error {
	blob, err := c.ProfileBlob(sel, seq)
	if err != nil {
		return err
	}
	if outFile == "" {
		_, err = out.Write(blob)
		return err
	}
	return os.WriteFile(outFile, blob, 0o644)
}

func newProfileCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{Use: "profile", Short: "Profile a node app's execution (opaque blob; decode is node-side)"}
	cmd.PersistentFlags().StringVar(&device, "device", "", "target node id or name")

	var duration time.Duration
	var continuous bool
	var label string
	start := &cobra.Command{Use: "start <app>", Short: "Arm a one-shot profile session", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runProfileStart(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0],
				int64(duration.Seconds()), continuous, label)
		}}
	start.Flags().DurationVar(&duration, "duration", 30*time.Second, "run-loop auto-stop bound")
	start.Flags().BoolVar(&continuous, "continuous", false, "re-arm each cycle until stop")
	start.Flags().StringVar(&label, "label", "", "operator label (porta-side only)")

	stop := &cobra.Command{Use: "stop <app>", Short: "Disarm a profile session", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runProfileStop(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0])
		}}

	var after int64
	poll := &cobra.Command{Use: "poll", Short: "List profile results (seq > --after)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProfilePoll(cmd.OutOrStdout(), apiclient.New(serverURL()), device, after)
		}}
	poll.Flags().Int64Var(&after, "after", 0, "only results with seq greater than this")

	var outFile string
	get := &cobra.Command{Use: "get <seq>", Short: "Fetch one result blob (raw) to stdout or file", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			seq, err := parseInt64(a[0])
			if err != nil {
				return err
			}
			return runProfileGet(cmd.OutOrStdout(), apiclient.New(serverURL()), device, seq, outFile)
		}}
	get.Flags().StringVarP(&outFile, "output", "o", "", "write blob to this file instead of stdout")

	cmd.AddCommand(start, stop, poll, get)
	return cmd
}

// parseInt64 is a tiny helper for the get <seq> arg.
func parseInt64(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscan(s, &v)
	return v, err
}
