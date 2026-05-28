// internal/portacli/serve_test.go
package portacli

import (
	"testing"
)

func TestDefaultAllowCIDRHasRFC1918AndLoopback(t *testing.T) {
	got := defaultAllowCIDR()
	want := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
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
