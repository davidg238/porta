package portacli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

// relativeAge is a thin wrapper around control.RelativeAge for internal use.
func relativeAge(ts, now int64) string { return control.RelativeAge(ts, now) }

// App is one entry from a node's observed apps map.
// Re-exported from control for portacli internal tests.
type App = control.App

// appsFromObserved is a thin wrapper around control.AppsFromObserved for internal use.
func appsFromObserved(observed string) ([]App, error) { return control.AppsFromObserved(observed) }

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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			nodes, err := st.ListNodes()
			if err != nil {
				return err
			}
			now := nowSec()
			for _, n := range nodes {
				if !n.LastSeen.Valid && !includeNeverSeen {
					continue
				}
				status := "offline"
				if n.Online(now) {
					status = "online"
				}
				seen := relativeAge(0, now)
				if n.LastSeen.Valid {
					seen = relativeAge(n.LastSeen.Int64, now)
				}
				fmt.Printf("%-12s  %-16s  %-12s  %s\n", n.ID, n.Name, seen, status)
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			n, err := st.GetNode(id)
			if err != nil || n == nil {
				return fmt.Errorf("node %s not found", id)
			}
			if n.Online(nowSec()) {
				fmt.Printf("%s (%s): online\n", n.Name, id)
			} else {
				fmt.Printf("%s (%s): offline\n", n.Name, id)
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			cmds, err := st.CommandLog(id)
			if err != nil {
				return err
			}
			for _, c := range cmds {
				delivered := "pending"
				if c.DeliveredAt.Valid {
					delivered = "yes"
				}
				fmt.Printf("#%-4d %-18s delivered=%-7s %s\n", c.ID, c.Verb, delivered, c.Args)
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
		newDeviceSetConsoleCmd(),
		newDeviceSetPollIntervalCmd(),
		newDeviceSetMaxOfflineCmd(),
		newDeviceNameCmd(),
	)
	return parent
}

func newDeviceShowCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show node details",
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
			n, err := st.GetNode(id)
			if err != nil || n == nil {
				return fmt.Errorf("node %s not found", id)
			}
			now := nowSec()
			fmt.Printf("id:            %s\n", n.ID)
			fmt.Printf("name:          %s\n", n.Name)
			fmt.Printf("kind:          %s\n", n.Kind)
			fmt.Printf("source_addr:   %s\n", n.SourceAddr)
			lastSeen := "never"
			if n.LastSeen.Valid {
				lastSeen = relativeAge(n.LastSeen.Int64, now)
			}
			fmt.Printf("last_seen:     %s\n", lastSeen)
			fmt.Printf("poll_interval: %ds\n", n.PollIntervalS)
			fmt.Printf("max_offline:   %ds\n", n.MaxOfflineS)
			fmt.Printf("observed:      %s\n", n.ObservedState)
			un, _ := st.UndeliveredCommands(id)
			fmt.Printf("undelivered:   %d command(s)\n", len(un))
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			n, err := st.GetNode(id)
			if err != nil || n == nil {
				return fmt.Errorf("node %s not found", id)
			}
			apps, err := appsFromObserved(n.ObservedState)
			if err != nil {
				return err
			}
			for _, a := range apps {
				fmt.Printf("%-16s crc=%-12d runlevel=%d\n", a.Name, a.CRC, a.Runlevel)
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

// runDeviceGet is the testable core of `porta device get`. If key is empty,
// it renders a table over the union of desired ∪ observed keys for app;
// otherwise it renders the single-key one-liner. Either form prints a ≥2×
// self-heal warning footer for each still-divergent key.
func runDeviceGet(out io.Writer, st *store.Store, id, app, key string) error {
	rows, err := control.DesiredVsObserved(st, id, app)
	if err != nil {
		return err
	}
	render := func(r control.ConfigRow) (string, string) {
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			key := ""
			if len(args) == 2 {
				key = args[1]
			}
			return runDeviceGet(cmd.OutOrStdout(), st, id, args[0], key)
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
