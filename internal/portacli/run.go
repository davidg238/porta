package portacli

import (
	"bytes"
	"fmt"
	"io"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/toolchain"
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
