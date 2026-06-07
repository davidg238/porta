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

// onOffStr converts a bool to "on" or "off".
func onOffStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// parseOnOff accepts "on" or "off" and returns the corresponding bool.
func parseOnOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("expected on or off, got %q", s)
}

// runDeviceSetForward enqueues a set-forward command carrying the complete
// per-stream policy. set-forward is absolute, so the CLI requires all three
// stream states explicitly (no silent off). The log level defaults to warn
// on the node when omitted.
func runDeviceSetForward(out io.Writer, c *apiclient.Client, sel string, print, log, telemetry bool, logLevel string) error {
	logPolicy := map[string]any{"on": log}
	if logLevel != "" {
		logPolicy["level"] = logLevel
	}
	args := map[string]any{
		"print":     map[string]any{"on": print},
		"log":       logPolicy,
		"telemetry": map[string]any{"on": telemetry},
	}
	cmdID, nodeID, err := c.Command(sel, "set-forward", args)
	if err != nil {
		return err
	}
	lvl := logLevel
	if lvl == "" {
		lvl = "warn"
	}
	fmt.Fprintf(out, "%s: enqueued set-forward (command #%d)\n  → print:%s  log:%s[%s]  telemetry:%s\n",
		nodeID, cmdID, onOffStr(print), onOffStr(log), lvl, onOffStr(telemetry))
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

// runDeviceReboot enqueues a reboot command (no args). The node reboots at the
// end of its next poll; there is no observed-state convergence to confirm.
func runDeviceReboot(out io.Writer, c *apiclient.Client, sel string) error {
	cmdID, nodeID, err := c.Command(sel, "reboot", nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued reboot (command #%d)\n", nodeID, cmdID)
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

func newDeviceRebootCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "reboot",
		Short: "Reboot a node (applied at the end of its next poll)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := apiclient.New(serverURL())
			return runDeviceReboot(cmd.OutOrStdout(), c, device)
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetForwardCmd() *cobra.Command {
	var device, printS, logS, telemetryS, logLevel string
	cmd := &cobra.Command{
		Use:   "set-forward",
		Short: "Set a node's per-stream forwarding policy (absolute — all streams required)",
		Long: "Set the complete per-stream forwarding policy. set-forward is absolute: " +
			"every stream you do not enable is turned OFF, so --print, --log and --telemetry " +
			"are all required.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			print, err := parseOnOff(printS)
			if err != nil {
				return fmt.Errorf("--print: %w", err)
			}
			log, err := parseOnOff(logS)
			if err != nil {
				return fmt.Errorf("--log: %w", err)
			}
			telemetry, err := parseOnOff(telemetryS)
			if err != nil {
				return fmt.Errorf("--telemetry: %w", err)
			}
			c := apiclient.New(serverURL())
			return runDeviceSetForward(cmd.OutOrStdout(), c, device, print, log, telemetry, logLevel)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&printS, "print", "", "forward print stream (on|off)")
	cmd.Flags().StringVar(&logS, "log", "", "forward log stream (on|off)")
	cmd.Flags().StringVar(&telemetryS, "telemetry", "", "forward telemetry/metric stream (on|off)")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "minimum log level (trace|debug|info|warn|error|fatal; node default warn)")
	_ = cmd.MarkFlagRequired("print")
	_ = cmd.MarkFlagRequired("log")
	_ = cmd.MarkFlagRequired("telemetry")
	return cmd
}
