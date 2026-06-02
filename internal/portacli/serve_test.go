// internal/portacli/serve_test.go
package portacli

import (
	"context"
	"errors"
	"testing"
	"time"
)

// errSentinel is a recognizable listener error for awaitServeExit tests.
var errSentinel = errors.New("sentinel listener failure")

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

// TestAwaitServeExitDrainsOnCtxCancel is the #7 guard: on SIGINT (ctx
// cancel) the serve must wait for BOTH listener goroutines to finish their
// graceful shutdown — draining udpErr and httpErr — before returning, rather
// than racing the deferred conn/store Close.
func TestAwaitServeExitDrainsOnCtxCancel(t *testing.T) {
	udpErr := make(chan error, 1)
	httpErr := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // SIGINT analog: ctx.Done fires immediately

	done := make(chan error, 1)
	go func() { done <- awaitServeExit(ctx, udpErr, httpErr) }()

	// Must not return while either listener is still shutting down.
	select {
	case <-done:
		t.Fatal("returned before draining udpErr/httpErr")
	case <-time.After(50 * time.Millisecond):
	}

	// Listeners report clean shutdown; only now may awaitServeExit return.
	udpErr <- nil
	httpErr <- nil
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not return after both channels drained")
	}
}

// TestAwaitServeExitNilHTTPErr covers --http-port 0: httpErr is nil. The
// ctx-cancel drain must not deadlock waiting on a nil channel.
func TestAwaitServeExitNilHTTPErr(t *testing.T) {
	udpErr := make(chan error, 1)
	var httpErr chan error // nil, like --http-port 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- awaitServeExit(ctx, udpErr, httpErr) }()
	udpErr <- nil
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadlocked draining a nil httpErr")
	}
}

// TestAwaitServeExitPropagatesListenerErrors confirms a listener failure (no
// ctx cancel) is returned verbatim, and a clean HTTP exit falls through to
// wait on UDP.
func TestAwaitServeExitPropagatesListenerErrors(t *testing.T) {
	ctx := context.Background()

	// UDP listener fails → its error propagates.
	udpErr := make(chan error, 1)
	udpErr <- errSentinel
	if got := awaitServeExit(ctx, udpErr, nil); got != errSentinel {
		t.Errorf("udp error: got %v, want errSentinel", got)
	}

	// HTTP fails → its error propagates.
	udpErr = make(chan error, 1)
	httpErr := make(chan error, 1)
	httpErr <- errSentinel
	if got := awaitServeExit(ctx, udpErr, httpErr); got != errSentinel {
		t.Errorf("http error: got %v, want errSentinel", got)
	}

	// HTTP exits cleanly while UDP still running → fall through to UDP.
	udpErr = make(chan error, 1)
	httpErr = make(chan error, 1)
	httpErr <- nil
	udpErr <- errSentinel
	if got := awaitServeExit(ctx, udpErr, httpErr); got != errSentinel {
		t.Errorf("clean http then udp error: got %v, want errSentinel", got)
	}
}
