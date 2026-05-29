package portacli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/spf13/cobra"
)

type installOpts struct {
	CRC       int64 // 0 → compute from file
	IntervalS int64
	Triggers  []string
	Runlevel  int
	Lifecycle string
}

// runInstall reads a .bin, registers it under its CRC32-IEEE, and enqueues a run.
func runInstall(st *store.Store, id, name, path string, opts installOpts, now int64) error {
	if !strings.HasSuffix(path, ".bin") {
		return fmt.Errorf("unsupported file %q (B1 accepts only prebuilt .bin)", path)
	}
	// Read the file first so we can compute the CRC for the confirmation printf.
	img, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Resolve CRC before delegating so we can print the exact value.
	crc := opts.CRC
	if crc == 0 {
		crc = int64(command.CRC32(img))
	}
	// Warn early if no triggers were given.
	triggers, err := command.TriggersFromFlags(opts.Triggers, opts.IntervalS)
	if err != nil {
		return err
	}
	if len(triggers) == 0 {
		fmt.Printf("note: no triggers given — %q installed but not started\n", name)
	}
	cmdID, err := control.Install(st, id, name, bytes.NewReader(img), control.InstallOpts{
		CRC: crc, IntervalS: opts.IntervalS, Triggers: opts.Triggers,
		Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	}, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: registered %s@%d (%d B); enqueued run (command #%d)\n", id, name, crc, len(img), cmdID)
	return nil
}

func runUninstall(st *store.Store, id, name string, now int64) error {
	cmdID, err := control.Uninstall(st, id, name, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: enqueued stop %s (command #%d)\n", id, name, cmdID)
	return nil
}

func runSetPollInterval(st *store.Store, id string, secs, now int64) error {
	_, err := control.SetPollInterval(st, id, secs, "cli", now)
	return err
}

// --- cobra wiring (attached to the parents from inspect.go) ---

func newContainerInstallCmd() *cobra.Command {
	var device string
	var opts installOpts
	var interval string
	cmd := &cobra.Command{
		Use:   "install <name> <file.bin>",
		Short: "Register a prebuilt image and enqueue run",
		Args:  cobra.ExactArgs(2),
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
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			if interval != "" {
				if opts.IntervalS, err = command.ParseDurationSeconds(interval); err != nil {
					return err
				}
			}
			if opts.Lifecycle == "" {
				opts.Lifecycle = "run-once"
			}
			return runInstall(st, id, args[0], args[1], opts, nowSec())
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().Int64Var(&opts.CRC, "crc", 0, "override the computed CRC32")
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			return runUninstall(st, id, args[0], nowSec())
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

func newDeviceSetPollIntervalCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-poll-interval <dur>",
		Short: "Enqueue a poll-interval change (and cache it)",
		Args:  cobra.ExactArgs(1),
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
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			secs, err := command.ParseDurationSeconds(args[0])
			if err != nil {
				return err
			}
			return runSetPollInterval(st, id, secs, nowSec())
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			secs, err := command.ParseDurationSeconds(args[0])
			if err != nil {
				return err
			}
			return control.SetMaxOffline(st, id, secs)
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
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			return control.Rename(st, id, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// runDeviceSet is the testable core of `porta device set`: it infers the
// scalar type from the operator's string, enqueues a set command tagged
// issued_by="cli", and prints a confirmation line to out.
func runDeviceSet(out io.Writer, st *store.Store, id, app, key, valueStr string, now int64) error {
	value := config.InferScalar(valueStr)
	cmdID, err := control.Set(st, id, app, key, value, "cli", now)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set %s.%s=%v (command #%d)\n", id, app, key, value, cmdID)
	return nil
}

func newDeviceSetCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set <app> <key> <value>",
		Short: "Enqueue a per-app config write (set verb)",
		Args:  cobra.ExactArgs(3),
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
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			return runDeviceSet(cmd.OutOrStdout(), st, id, args[0], args[1], args[2], nowSec())
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}

// runDeviceSetConsole is the testable core of `porta device set-console`:
// it validates the state token, enqueues a set-console command tagged
// issued_by="cli", and prints a confirmation line.
func runDeviceSetConsole(out io.Writer, st *store.Store, id, state string, now int64) error {
	var on bool
	switch state {
	case "on":
		on = true
	case "off":
		on = false
	default:
		return fmt.Errorf("set-console: state must be 'on' or 'off', got %q", state)
	}
	cmdID, err := control.SetConsole(st, id, on, "cli", now)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set-console %s (command #%d)\n", id, state, cmdID)
	return nil
}

func newDeviceSetConsoleCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set-console <on|off>",
		Short: "Toggle a node's console/telemetry forwarding",
		Args:  cobra.ExactArgs(1),
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
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			return runDeviceSetConsole(cmd.OutOrStdout(), st, id, args[0], nowSec())
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
