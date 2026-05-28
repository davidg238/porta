package portacli

import (
	"encoding/json"
	"fmt"

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
		newDeviceSetCmd(),
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
