package portacli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

// relativeAge renders an epoch-seconds timestamp relative to now.
func relativeAge(ts, now int64) string {
	if ts == 0 {
		return "never"
	}
	d := now - ts
	switch {
	case d <= 60:
		return fmt.Sprintf("%ds ago", d)
	case d <= 3600:
		return fmt.Sprintf("%dm ago", d/60)
	case d < 86400:
		return fmt.Sprintf("%dh ago", d/3600)
	default:
		return fmt.Sprintf("%dd ago", d/86400)
	}
}

// App is one entry from a node's observed apps map.
type App struct {
	Name     string
	CRC      int64
	Runlevel int64
}

// appsFromObserved decodes the apps map from a cached observed_state JSON blob.
func appsFromObserved(observed string) ([]App, error) {
	if observed == "" {
		return nil, nil
	}
	var obj struct {
		Apps map[string]struct {
			CRC      int64 `json:"crc"`
			Runlevel int64 `json:"runlevel"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(observed), &obj); err != nil {
		return nil, err
	}
	var out []App
	for name, a := range obj.Apps {
		out = append(out, App{Name: name, CRC: a.CRC, Runlevel: a.Runlevel})
	}
	return out, nil
}

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

// configFromObserved decodes a node's cached observed_state JSON into the
// app→{key:value} map for config display + comparison. Uses UseNumber() so
// values match the desired side under EqualScalars.
func configFromObserved(observed string) map[string]map[string]any {
	if observed == "" {
		return map[string]map[string]any{}
	}
	var obj struct {
		Config map[string]map[string]any `json:"config"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(observed)))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj.Config == nil {
		return map[string]map[string]any{}
	}
	return obj.Config
}

// renderScalar formats a scalar for the desired/observed cells. json.Number
// prints as its canonical text; bool/string print as-is; nil renders as "--".
func renderScalar(v any) string {
	if v == nil {
		return "--"
	}
	return fmt.Sprintf("%v", v)
}

// unionKeys returns a sorted slice of all keys present in either map.
func unionKeys(a, b map[string]any) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// printWarnings emits the ≥2× self-heal footer for each still-divergent key.
func printWarnings(out io.Writer, id, app string, keys []string, desired, observed map[string]any, cmds []store.Command) {
	for _, k := range keys {
		d, dOK := desired[k]
		o, oOK := observed[k]
		if config.Marker(d, o, dOK, oOK) == "" {
			continue
		}
		if n := config.ReconcileCount(cmds, app, k); n >= 2 {
			fmt.Fprintf(out, "%s: ⚠ %s.%s: self-healed %d× — node may be failing to apply\n", id, app, k, n)
		}
	}
}

// runDeviceGet is the testable core of `porta device get`. If key is empty,
// it renders a table over the union of desired ∪ observed keys for app;
// otherwise it renders the single-key one-liner. Either form prints a ≥2×
// self-heal warning footer for each still-divergent key.
func runDeviceGet(out io.Writer, st *store.Store, id, app, key string) error {
	n, err := st.GetNode(id)
	if err != nil || n == nil {
		return fmt.Errorf("node %s not found", id)
	}
	cmds, err := st.CommandLog(id)
	if err != nil {
		return err
	}
	desired := config.ProjectDesiredForApp(cmds, app)
	observed := configFromObserved(n.ObservedState)[app]
	if observed == nil {
		observed = map[string]any{}
	}

	if key != "" {
		d, dOK := desired[key]
		o, oOK := observed[key]
		marker := config.Marker(d, o, dOK, oOK)
		line := fmt.Sprintf("%s: %s.%s desired=%s observed=%s", id, app, key, renderScalar(d), renderScalar(o))
		if marker != "" {
			line += " " + marker
		}
		fmt.Fprintln(out, line)
		printWarnings(out, id, app, []string{key}, desired, observed, cmds)
		return nil
	}

	// Multi-key: union of desired ∪ observed, sorted.
	keys := unionKeys(desired, observed)
	fmt.Fprintf(out, "%s: config for %s\n", id, app)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  KEY\tDESIRED\tOBSERVED\t")
	for _, k := range keys {
		d, dOK := desired[k]
		o, oOK := observed[k]
		marker := config.Marker(d, o, dOK, oOK)
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", k, renderScalar(d), renderScalar(o), marker)
	}
	w.Flush()
	printWarnings(out, id, app, keys, desired, observed, cmds)
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
