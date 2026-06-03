package portacli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidg238/porta/internal/apiclient"
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

// runDeploy is the testable core of `porta run`: SDK guard (read the node's
// reported sdk via the API, compare against the local toolchain), local build,
// then deliver the image + enqueue run via the control-plane API. force skips
// the SDK match refusal (but not the unknown-identity block). The server stamps
// issued_by="api".
func runDeploy(out io.Writer, c *apiclient.Client, ex *toolchain.Executor, sel, appPath string, opts deployOpts, force bool) error {
	_, sdk, err := c.NodeIdentity(sel)
	if err != nil {
		return err
	}
	if sdk == "" {
		return fmt.Errorf("node %s hasn't reported its firmware identity yet — wait for a check-in (or flash it via `porta flash`) before deploying", sel)
	}
	active, err := toolchain.SDKVersion(ex)
	if err != nil {
		return err
	}
	if !force {
		if err := toolchain.CheckSDK(sdk, active); err != nil {
			return err
		}
	}
	img, err := toolchain.Build(ex, appPath)
	if err != nil {
		return err
	}
	cmdID, nodeID, size, err := c.Install(sel, opts.Name, bytes.NewReader(img), apiclient.InstallOpts{
		Lifecycle: opts.Lifecycle, Runlevel: opts.Runlevel, Triggers: opts.Triggers,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: built %s (%d B), enqueued run (command #%d)\n", nodeID, opts.Name, size, cmdID)
	if opts.PowerMode != "" {
		if _, _, err := c.Command(sel, "set-power-mode", map[string]any{"mode": opts.PowerMode}); err != nil {
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
			c := apiclient.New(serverURL())
			ex := toolchain.NewExecutor(toolchain.ExecRunner{}, cmd.OutOrStdout(), verbose)
			return runDeploy(cmd.OutOrStdout(), c, ex, device, appPath, opts, force)
		},
	}
	deviceFlag(cmd, &device)
	cmd.Flags().StringVar(&opts.Name, "name", "", "container name (default: source file stem)")
	cmd.Flags().StringVar(&opts.Lifecycle, "lifecycle", "", "run-once or run-loop (prompted if unset)")
	cmd.Flags().StringArrayVar(&opts.Triggers, "trigger", nil, "trigger spec (boot, gpio-high=21, …); prompted if unset")
	cmd.Flags().IntVar(&opts.Runlevel, "runlevel", 3, "container start order (default 3)")
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
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	return strings.Fields(line)
}
