// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"fmt"
	"io"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/spf13/cobra"
)

func runDebugAttach(out io.Writer, c *apiclient.Client, sel, app string) error {
	cid, node, err := c.DebugAttach(sel, app)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: debug attach %s (command #%d)\n", node, app, cid)
	return nil
}

func runDebugDetach(out io.Writer, c *apiclient.Client, sel, app string) error {
	cid, node, err := c.DebugDetach(sel, app)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: debug detach %s (command #%d)\n", node, app, cid)
	return nil
}

func runDebugSend(out io.Writer, c *apiclient.Client, sel, line string) error {
	if err := c.DebugSend(sel, line); err != nil {
		return err
	}
	fmt.Fprintf(out, "sent: %s\n", line)
	return nil
}

func runDebugPoll(out io.Writer, c *apiclient.Client, sel string, after int64) (int64, error) {
	rows, err := c.DebugResponses(sel, after)
	if err != nil {
		return after, err
	}
	for _, r := range rows {
		fmt.Fprintln(out, r.Line)
		after = r.ID
	}
	return after, nil
}

func newDebugCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{Use: "debug", Short: "Remote-debug a node over porta (dbg:* bridge)"}
	cmd.PersistentFlags().StringVar(&device, "device", "", "target node id or name")

	attach := &cobra.Command{Use: "attach <app>", Short: "Attach the debugger to an app", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runDebugAttach(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0])
		}}
	detach := &cobra.Command{Use: "detach <app>", Short: "Detach the debugger", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runDebugDetach(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0])
		}}
	send := &cobra.Command{Use: "send <dbg-line>", Short: "Send one dbg: request line", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return runDebugSend(cmd.OutOrStdout(), apiclient.New(serverURL()), device, a[0])
		}}
	var after int64
	poll := &cobra.Command{Use: "poll", Short: "Print new dbg: response lines (id > --after)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := runDebugPoll(cmd.OutOrStdout(), apiclient.New(serverURL()), device, after)
			return err
		}}
	poll.Flags().Int64Var(&after, "after", 0, "only responses with id greater than this")
	cmd.AddCommand(attach, detach, send, poll)
	return cmd
}
