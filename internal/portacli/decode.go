// internal/portacli/decode.go
package portacli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/internal/toolchain"
)

// panicDecoder symbolicates a base64 trace blob into a readable stack trace.
// The seam keeps runMonitor and the panic commands unit-testable with a fake.
type panicDecoder interface {
	Decode(blob string) (string, error)
}

// jagDecoder shells out to `jag decode <blob>`, which resolves the blob's
// embedded program UUID against jag's local snapshot cache (populated by
// `porta run`, see toolchain.RetainSnapshot). It uses a plain Runner (not the
// narrating Executor) so decode adds no "→ jag decode …" noise to monitor output.
type jagDecoder struct{ r toolchain.Runner }

// newJagDecoder builds the production decoder over os/exec.
func newJagDecoder() jagDecoder { return jagDecoder{r: toolchain.ExecRunner{}} }

func (d jagDecoder) Decode(blob string) (string, error) {
	out, err := d.r.Run("jag", "decode", blob)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return "", fmt.Errorf("jag decode: %w: %s", err, msg)
		}
		return "", fmt.Errorf("jag decode: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// panicTime formats a panic row's epoch-seconds timestamp for display.
func panicTime(ts int64) string {
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

// indentLines prefixes every line of s with prefix.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// renderPanic writes a panic row: a "‼ PANIC <time>" header, then the decoded
// trace (indented) or — on decode failure or a nil decoder — the raw blob plus
// a hint that the snapshot lives where the image was built.
func renderPanic(out io.Writer, r apiclient.DataRow, dec panicDecoder) {
	fmt.Fprintf(out, "‼ PANIC  %s\n", panicTime(r.TS))
	if dec != nil {
		if trace, err := dec.Decode(r.Text); err == nil {
			fmt.Fprintln(out, indentLines(strings.TrimRight(trace, "\n"), "  "))
			return
		}
	}
	fmt.Fprintf(out, "  (no local snapshot — decode where the image was built)\n  jag decode %s\n", r.Text)
}

// panicSummary is the one-line summary for `panic list`: the first non-empty
// decoded line, or a fallback marker when it cannot decode.
func panicSummary(r apiclient.DataRow, dec panicDecoder) string {
	if dec != nil {
		if trace, err := dec.Decode(r.Text); err == nil {
			for _, l := range strings.Split(trace, "\n") {
				if s := strings.TrimSpace(l); s != "" {
					return s
				}
			}
		}
	}
	return "(no local snapshot — decode where built)"
}
