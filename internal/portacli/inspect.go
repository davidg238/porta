// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/internal/control"
	"github.com/spf13/cobra"
)

// App is one entry from a node's observed apps map.
// Re-exported from control for portacli internal tests.
type App = control.App

// deviceFlag adds and reads the shared -d/--device flag.
func deviceFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVarP(dst, "device", "d", "", "node name or MAC")
	cmd.MarkFlagRequired("device")
}

func newScanCmd() *cobra.Command {
	var includeNeverSeen bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "List nodes (online/offline)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := apiclient.New(serverURL())
			nodes, err := c.ListNodes()
			if err != nil {
				return err
			}
			now := nowSec()
			for _, n := range nodes {
				if n.LastSeen == 0 && !includeNeverSeen {
					continue
				}
				status := "offline"
				if n.Online {
					status = "online"
				}
				seen := control.RelativeAge(n.LastSeen, now)
				fmt.Fprintf(cmd.OutOrStdout(), "%-16s  %-16s  %-12s  %s\n", n.ID, n.Name, seen, status)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&includeNeverSeen, "include-never-seen", false, "show nodes that never contacted")
	return cmd
}

func newPingCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Report whether a node is online",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := apiclient.New(serverURL())
			n, err := c.NodeDetail(device)
			if err != nil {
				return err
			}
			if n.Online {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s): online\n", n.Name, n.ID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%s): offline\n", n.Name, n.ID)
			}
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newLogCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Command audit history",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := apiclient.New(serverURL())
			cmds, err := c.NodeCommands(device)
			if err != nil {
				return err
			}
			// The API returns newest-first; the store-backed log printed
			// oldest-first (ORDER BY id). Reverse to preserve that order.
			for i := len(cmds) - 1; i >= 0; i-- {
				lc := cmds[i]
				delivered := "pending"
				if lc.Delivered {
					delivered = "yes"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "#%-4d %-18s delivered=%-7s %s\n", lc.ID, lc.Verb, delivered, lc.Args)
			}
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// newDeviceCmd builds the `device` parent with show + mutation subcommands.
func newDeviceCmd() *cobra.Command {
	parent := &cobra.Command{Use: "device", Short: "Per-node operations"}
	parent.AddCommand(
		newDeviceShowCmd(),
		newDeviceGetCmd(),
		newDeviceSetCmd(),
		newDeviceSetForwardCmd(),
		newDeviceRebootCmd(),
	)
	return parent
}

func newDeviceShowCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show node details",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := apiclient.New(serverURL())
			n, err := c.NodeDetail(device)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			now := nowSec()
			fmt.Fprintf(out, "id:            %s\n", n.ID)
			fmt.Fprintf(out, "name:          %s\n", n.Name)
			fmt.Fprintf(out, "kind:          %s\n", n.Kind)
			fmt.Fprintf(out, "source_addr:   %s\n", n.IP)
			lastSeen := "never"
			if n.LastSeen != 0 {
				lastSeen = control.RelativeAge(n.LastSeen, now)
			}
			fmt.Fprintf(out, "last_seen:     %s\n", lastSeen)
			fmt.Fprintf(out, "cadence:       %ds\n", n.CadenceS)
			fmt.Fprintf(out, "offline_after: %ds (derived 3×cadence)\n", n.OfflineS)
			fmt.Fprintf(out, "last_reset:    %s\n", control.RenderReset(n.Reset, n.ResetCode))
			fmt.Fprintf(out, "observed:      %s\n", n.ObservedRaw)
			fmt.Fprintf(out, "undelivered:   %d command(s)\n", n.Undelivered)
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// newContainerCmd builds the `container` parent with list + mutation subcommands.
func newContainerCmd() *cobra.Command {
	parent := &cobra.Command{Use: "container", Short: "Container operations"}
	parent.AddCommand(
		newContainerListCmd(),
		newContainerInstallCmd(),
		newContainerUninstallCmd(),
	)
	return parent
}

func newContainerListCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List apps from the latest observed report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := apiclient.New(serverURL())
			n, err := c.NodeDetail(device)
			if err != nil {
				return err
			}
			for _, a := range n.Apps {
				fmt.Fprintf(cmd.OutOrStdout(), "%-16s crc=%-12d runlevel=%d\n", a.Name, a.CRC, a.Runlevel)
			}
			return nil
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// renderScalar formats a scalar for the desired/observed cells. json.Number
// prints as its canonical text; bool/string print as-is; nil renders as "--".
func renderScalar(v any) string {
	if v == nil {
		return "--"
	}
	return fmt.Sprintf("%v", v)
}

// runDeviceGet is the testable core of `porta device get`. It sources the
// desired-vs-observed rows from the control-plane API (the server resolves the
// selector and echoes id). If key is empty, it renders a table over the union
// of desired ∪ observed keys for app; otherwise it renders the single-key
// one-liner. Either form prints a ≥2× self-heal warning footer for each
// still-divergent key.
func runDeviceGet(out io.Writer, c *apiclient.Client, sel, app, key string) error {
	n, err := c.NodeDetail(sel)
	if err != nil {
		return err
	}
	id := n.ID
	rows, err := c.Config(sel, app)
	if err != nil {
		return err
	}
	render := func(r apiclient.ConfigRow) (string, string) {
		ds, os := "--", "--"
		if r.DesiredPresent {
			ds = renderScalar(r.Desired)
		}
		if r.ObservedPresent {
			os = renderScalar(r.Observed)
		}
		return ds, os
	}

	if key != "" {
		for _, r := range rows {
			if r.Key != key {
				continue
			}
			ds, os := render(r)
			line := fmt.Sprintf("%s: %s.%s desired=%s observed=%s", id, app, key, ds, os)
			if r.Marker != "" {
				line += " " + r.Marker
			}
			fmt.Fprintln(out, line)
			if r.ReissueCount >= 2 {
				fmt.Fprintf(out, "%s: ⚠ %s.%s: self-healed %d× — node may be failing to apply\n", id, app, key, r.ReissueCount)
			}
			return nil
		}
		fmt.Fprintf(out, "%s: %s.%s desired=-- observed=--\n", id, app, key)
		return nil
	}

	fmt.Fprintf(out, "%s: config for %s\n", id, app)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  KEY\tDESIRED\tOBSERVED\t")
	for _, r := range rows {
		ds, os := render(r)
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.Key, ds, os, r.Marker)
	}
	w.Flush()
	for _, r := range rows {
		if r.Marker != "" && r.ReissueCount >= 2 {
			fmt.Fprintf(out, "%s: ⚠ %s.%s: self-healed %d× — node may be failing to apply\n", id, app, r.Key, r.ReissueCount)
		}
	}
	return nil
}

// newDeviceGetCmd builds the `device get` subcommand.
func newDeviceGetCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "get <app> [key]",
		Short: "Show desired vs observed config for an app (or one key)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			key := ""
			if len(args) == 2 {
				key = args[1]
			}
			return runDeviceGet(cmd.OutOrStdout(), c, device, args[0], key)
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
