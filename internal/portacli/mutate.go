package portacli

import (
	"fmt"
	"os"
	"strings"

	"github.com/davidg238/porta/internal/command"
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
	img, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	crc := opts.CRC
	if crc == 0 {
		crc = int64(command.CRC32(img))
	}
	triggers, err := command.TriggersFromFlags(opts.Triggers, opts.IntervalS)
	if err != nil {
		return err
	}
	if len(triggers) == 0 {
		fmt.Printf("note: no triggers given — %q installed but not started\n", name)
	}
	runCmd, err := command.Run(command.RunSpec{
		Name: name, CRC: crc, Size: int64(len(img)),
		Triggers: triggers, Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	})
	if err != nil {
		return err
	}
	if err := st.RegisterPayload(crc, name, img); err != nil {
		return err
	}
	cmdID, err := st.EnqueueCommand(id, runCmd.Verb, runCmd.ArgsJSON, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: registered %s@%d (%d B); enqueued run (command #%d)\n", id, name, crc, len(img), cmdID)
	return nil
}

func runUninstall(st *store.Store, id, name string, now int64) error {
	stop := command.Stop(name)
	cmdID, err := st.EnqueueCommand(id, stop.Verb, stop.ArgsJSON, "cli", now)
	if err != nil {
		return err
	}
	fmt.Printf("%s: enqueued stop %s (command #%d)\n", id, name, cmdID)
	return nil
}

func runSetPollInterval(st *store.Store, id string, secs, now int64) error {
	if err := st.SetPollInterval(id, secs); err != nil {
		return err
	}
	c := command.SetPollInterval(secs)
	_, err := st.EnqueueCommand(id, c.Verb, c.ArgsJSON, "cli", now)
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
			return st.SetMaxOffline(id, secs)
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
			return st.SetNodeName(id, args[0])
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
