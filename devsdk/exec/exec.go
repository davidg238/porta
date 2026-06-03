// Package exec is porta's injectable, narrating runner for external dev tools
// ("trainer wheels"): Runner abstracts the shell-out (tests inject a fake);
// Executor narrates each command (apt-style summary, or full transcript when
// verbose). Promoted from internal/toolchain so node-repo dev tools can reuse it.
package exec

import (
	"fmt"
	"io"
	osexec "os/exec"
	"strings"
	"time"
)

// Runner executes an external command and returns its combined stdout.
// The real implementation shells out; tests inject a fake.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands via os/exec, returning combined output.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) ([]byte, error) {
	return osexec.Command(name, args...).CombinedOutput()
}

// Executor narrates and runs commands. When verbose, child output is written
// to out as it returns; otherwise only a tidy per-step summary is shown.
type Executor struct {
	r       Runner
	out     io.Writer
	verbose bool
	now     func() time.Time
}

// NewExecutor builds an Executor over r, narrating to out.
func NewExecutor(r Runner, out io.Writer, verbose bool) *Executor {
	return &Executor{r: r, out: out, verbose: verbose, now: time.Now}
}

// Run announces (label + exact argv), executes, and reports success/failure.
// On failure it prints the rerunnable command so the operator can retry by hand.
func (e *Executor) Run(label, name string, args ...string) ([]byte, error) {
	cmdline := name + " " + strings.Join(args, " ")
	fmt.Fprintf(e.out, "→ %s\n  %s\n", label, cmdline)
	start := e.now()
	out, err := e.r.Run(name, args...)
	if e.verbose && len(out) > 0 {
		fmt.Fprintf(e.out, "%s\n", out)
	}
	if err != nil {
		fmt.Fprintf(e.out, "✗ %s — %v\n  rerun: %s\n", label, err, cmdline)
		if !e.verbose && len(out) > 0 {
			fmt.Fprintf(e.out, "%s\n", out)
		}
		return out, fmt.Errorf("%s: %w", label, err)
	}
	fmt.Fprintf(e.out, "✓ %s (%s)\n", label, e.now().Sub(start).Round(time.Millisecond))
	return out, nil
}
