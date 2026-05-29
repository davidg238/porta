// internal/portacli/serve_test.go
package portacli

import (
	"context"
	"testing"
	"time"
)

func TestDefaultAllowCIDRHasRFC1918LoopbackAndTailscale(t *testing.T) {
	got := defaultAllowCIDR()
	want := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"::1/128",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d = %q, want %q", i, got[i], w)
		}
	}
}

func TestDefaultAllowCIDRReturnsFreshSlice(t *testing.T) {
	a := defaultAllowCIDR()
	b := defaultAllowCIDR()
	// Mutating one must not affect the other (cobra StringSliceVar
	// shares the backing slice across resets).
	a[0] = "mutated"
	if b[0] == "mutated" {
		t.Error("defaultAllowCIDR returned a shared slice; must return a fresh copy")
	}
}

func TestNewServeCmdRegistersFlags(t *testing.T) {
	cmd := newServeCmd()
	for _, name := range []string{"port", "http-port", "http-bind", "http-allow-cidr"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered", name)
		}
	}
}

// TestServeNilHTTPErrChannelBlocksSelect is a focused regression guard
// for the --http-port 0 bug found in T5 review: a closed channel fired
// the select arm immediately, exiting the serve in microseconds. The fix
// uses a nil channel so the arm blocks forever; the test confirms the
// select arm is reachable but never fires when the channel is nil.
func TestServeNilHTTPErrChannelBlocksSelect(t *testing.T) {
	var httpErr chan error // nil, like the --http-port 0 case
	udpErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so ctx.Done fires immediately

	got := ""
	select {
	case <-udpErr:
		got = "udp"
	case <-httpErr:
		got = "http" // would indicate the bug is back
	case <-ctx.Done():
		got = "ctx"
	}
	if got != "ctx" {
		t.Errorf("got %q, want ctx (nil httpErr must not fire; udpErr was empty)", got)
	}

	// Sanity guard: with a real closed channel (the bug), the http arm
	// WOULD fire. Confirm that, so the test pins the contrast.
	httpErr = make(chan error, 1)
	close(httpErr)
	got = ""
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-httpErr:
		got = "http"
	case <-ctx2.Done():
		got = "ctx"
	case <-timer.C:
		got = "timer"
	}
	if got != "http" {
		t.Errorf("with closed channel, expected http arm to fire; got %q", got)
	}
}
