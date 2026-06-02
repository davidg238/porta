package portacli

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/toolchain"
	"github.com/spf13/cobra"
)

// deployOpts collects the knobs for a `porta run` deployment.
type deployOpts struct {
	Name      string
	Lifecycle string   // run-once | run-loop
	Triggers  []string
	Runlevel  int
	PowerMode string // "" → leave unchanged
}

// runDeploy is the testable core of `porta run`: identity + SDK guard, build,
// then register-payload + enqueue-run via control.Install. force skips the SDK
// match refusal.
func runDeploy(out io.Writer, st *store.Store, ex *toolchain.Executor, id, appPath string, opts deployOpts, force bool, now int64) error {
	node, err := st.GetNode(id)
	if err != nil {
		return err
	}
	if node == nil || node.Sdk == "" {
		return fmt.Errorf("node %s hasn't reported its firmware identity yet — wait for a check-in (or flash it via `porta flash`) before deploying", id)
	}
	active, err := toolchain.SDKVersion(ex)
	if err != nil {
		return err
	}
	if !force {
		if err := toolchain.CheckSDK(node.Sdk, active); err != nil {
			return err
		}
	}
	img, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
	cmdID, err := control.Install(st, id, opts.Name, bytes.NewReader(img), control.InstallOpts{
		Triggers: opts.Triggers, Runlevel: opts.Runlevel, Lifecycle: opts.Lifecycle,
	}, "cli", now)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: built %s (%d B), enqueued run (command #%d)\n", id, opts.Name, len(img), cmdID)
	if opts.PowerMode != "" {
		if _, err := control.SetPowerMode(st, id, opts.PowerMode, "cli", now); err != nil {
			return err
		}
	}
	return nil
}

func newRunCmd() *cobra.Command {
	var device string
	var opts deployOpts
	var force, verbose bool
	cmd := &cobra.Command{
		Use:   "run <app.toit>",
		Short: "Compile a Toit app, relocate it, and deploy it to a node (jag-run analog)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]
			if !strings.HasSuffix(appPath, ".toit") {
				return fmt.Errorf("expected a .toit source file, got %q", appPath)
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if opts.Name == "" {
				base := filepath.Base(appPath)
				opts.Name = strings.TrimSuffix(base, filepath.Ext(base))
			}
			// Prompt for the two run-shape answers; flags win when set.
			if opts.Lifecycle == "" {
				opts.Lifecycle = promptChoice("Lifecycle", []string{"run-once", "run-loop"}, "run-once")
			}
			if len(opts.Triggers) == 0 {
				opts.Triggers = promptTriggers()
			}
			ex := toolchain.NewExecutor(toolchain.ExecRunner{}, cmd.OutOrStdout(), verbose)
			return runDeploy(cmd.OutOrStdout(), st, ex, id, appPath, opts, force, nowSec())
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&opts.Name, "name", "", "container name (default: source file stem)")
	cmd.Flags().StringVar(&opts.Lifecycle, "lifecycle", "", "run-once or run-loop (prompted if unset)")
	cmd.Flags().StringArrayVar(&opts.Triggers, "trigger", nil, "trigger spec (boot, gpio-high=21, …); prompted if unset")
	cmd.Flags().IntVar(&opts.Runlevel, "runlevel", 3, "runlevel")
	cmd.Flags().StringVar(&opts.PowerMode, "power-mode", "", "deep-sleep or always-on (optional)")
	cmd.Flags().BoolVar(&force, "force", false, "deploy even if the build SDK differs from the node's")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "stream every underlying tool call")
	return cmd
}

// promptChoice asks the user to pick from options, returning def on empty input.
func promptChoice(label string, options []string, def string) string {
	fmt.Printf("%s %v [%s]: ", label, options, def)
	var in string
	fmt.Scanln(&in)
	in = strings.TrimSpace(in)
	for _, o := range options {
		if in == o {
			return o
		}
	}
	return def
}

// promptTriggers asks for a space-separated trigger list (empty → none).
func promptTriggers() []string {
	fmt.Print("Triggers (space-separated: boot, gpio-high=21, …; empty = none): ")
	var line string
	fmt.Scanln(&line)
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	return strings.Fields(line)
}
