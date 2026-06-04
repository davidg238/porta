// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/spf13/cobra"
)

// --- testable cores: each takes an *apiclient.Client and the RAW -d selector
// (the server resolves it); confirmation lines lead with the resolved node_id. ---

// runDeviceSet infers the scalar type from the operator's string and enqueues a
// set command via the API.
func runDeviceSet(out io.Writer, c *apiclient.Client, sel, app, key, valueStr string) error {
	value := config.InferScalar(valueStr)
	cmdID, nodeID, err := c.Command(sel, "set", map[string]any{"app": app, "key": key, "value": value})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set %s.%s=%v (command #%d)\n", nodeID, app, key, value, cmdID)
	return nil
}

// runDeviceSetConsole enqueues a set-console command. The on/off token is
// validated server-side.
func runDeviceSetConsole(out io.Writer, c *apiclient.Client, sel, state string) error {
	cmdID, nodeID, err := c.Command(sel, "set-console", map[string]any{"state": state})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set-console %s (command #%d)\n", nodeID, state, cmdID)
	return nil
}

// runDeviceSetPowerMode enqueues a set-power-mode command. The mode is validated
// server-side (command.SetPowerMode).
func runDeviceSetPowerMode(out io.Writer, c *apiclient.Client, sel, mode string) error {
	cmdID, nodeID, err := c.Command(sel, "set-power-mode", map[string]any{"mode": mode})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set-power-mode %s (command #%d)\n", nodeID, mode, cmdID)
	return nil
}

// runSetPollInterval enqueues a set-poll-interval command. The duration string
// is parsed server-side. Silent on success (parity with the pre-S2 CLI).
func runSetPollInterval(c *apiclient.Client, sel, dur string) error {
	_, _, err := c.Command(sel, "set-poll-interval", map[string]any{"interval": dur})
	return err
}

// runUninstall enqueues a stop command.
func runUninstall(out io.Writer, c *apiclient.Client, sel, name string) error {
	cmdID, nodeID, err := c.Command(sel, "stop", map[string]any{"name": name})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued stop %s (command #%d)\n", nodeID, name, cmdID)
	return nil
}

// runInstall uploads a prebuilt .bin via multipart; the server computes the CRC
// and registers the payload. The confirmation drops the CRC (visible via
// `porta log`) and uses the server-reported size.
func runInstall(out io.Writer, c *apiclient.Client, sel, name, path string, opts apiclient.InstallOpts) error {
	if !strings.HasSuffix(path, ".bin") {
		return fmt.Errorf("unsupported file %q (B1 accepts only prebuilt .bin)", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(opts.Triggers) == 0 && opts.IntervalS == 0 {
		fmt.Fprintf(out, "note: no triggers given — %q installed but not started\n", name)
	}
	cmdID, nodeID, size, err := c.Install(sel, name, f, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: registered %s (%d B); enqueued run (command #%d)\n", nodeID, name, size, cmdID)
	return nil
}

// runDeviceName renames a node (gateway-side). Silent on success (parity).
func runDeviceName(c *apiclient.Client, sel, newName string) error {
	_, err := c.PatchNode(sel, &newName, nil)
	return err
}

// runSetMaxOffline sets the offline threshold (gateway-side). Silent on success.
func runSetMaxOffline(c *apiclient.Client, sel string, secs int64) error {
	_, err := c.PatchNode(sel, nil, &secs)
	return err
}

// --- cobra wiring (attached to the parents from inspect.go) ---

func newContainerInstallCmd() *cobra.Command {
	var device string
	var opts apiclient.InstallOpts
	var interval string
	cmd := &cobra.Command{
		Use:   "install <name> <file.bin>",
		Short: "Register a prebuilt image and enqueue run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval != "" {
				var err error
				if opts.IntervalS, err = command.ParseDurationSeconds(interval); err != nil {
					return err
				}
			}
			if opts.Lifecycle == "" {
				opts.Lifecycle = "run-once"
			}
			c := apiclient.New(serverURL())
			return runInstall(cmd.OutOrStdout(), c, device, args[0], args[1], opts)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&interval, "interval", "", "interval trigger (e.g. 30s)")
	cmd.Flags().StringArrayVar(&opts.Triggers, "trigger", nil, "trigger spec (boot, gpio-high=21, …); repeatable")
	cmd.Flags().IntVar(&opts.Runlevel, "runlevel", 3, "runlevel")
	cmd.Flags().StringVar(&opts.Lifecycle, "lifecycle", "run-once", "run-once or run-loop")
	return cmd
}

func newContainerUninstallCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Enqueue stop for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runUninstall(cmd.OutOrStdout(), c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetPollIntervalCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-poll-interval <dur>",
		Short: "Enqueue a poll-interval change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runSetPollInterval(c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetMaxOfflineCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-max-offline <dur>",
		Short: "Set the offline threshold (gateway-side only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secs, err := command.ParseDurationSeconds(args[0])
			if err != nil {
				return err
			}
			c := apiclient.New(serverURL())
			return runSetMaxOffline(c, device, secs)
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceNameCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "name <new-name>",
		Short: "Override the auto-assigned friendly name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceName(c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set <app> <key> <value>",
		Short: "Enqueue a per-app config write (set verb)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceSet(cmd.OutOrStdout(), c, device, args[0], args[1], args[2])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetPowerModeCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-power-mode <deep-sleep|always-on>",
		Short: "Set a node's power mode (always-on keeps run-loop daemons alive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceSetPowerMode(cmd.OutOrStdout(), c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetConsoleCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-console <on|off>",
		Short: "Toggle a node's console/telemetry forwarding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := apiclient.New(serverURL())
			return runDeviceSetConsole(cmd.OutOrStdout(), c, device, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
