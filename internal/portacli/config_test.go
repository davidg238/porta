package portacli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestRunDeviceSetEnqueuesCli(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/c.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)

	var out bytes.Buffer
	if err := runDeviceSet(&out, st, "aabbccddeeff", "sampler", "interval", "30", 2000); err != nil {
		t.Fatal(err)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil {
		t.Fatal("expected a command")
	}
	if c.Verb != "set" {
		t.Errorf("verb=%q, want set", c.Verb)
	}
	if c.IssuedBy != "cli" {
		t.Errorf("issued_by=%q, want cli", c.IssuedBy)
	}
	if c.Args != `{"app":"sampler","key":"interval","value":30}` {
		t.Errorf("args=%s, want int-shaped 30", c.Args)
	}
	if !strings.Contains(out.String(), "enqueued set sampler.interval=30") {
		t.Errorf("stdout=%q, want enqueue message", out.String())
	}
}

func TestRunDeviceSetTypeInference(t *testing.T) {
	cases := []struct {
		name, value, wantArgs string
	}{
		{"int", "30", `{"app":"a","key":"k","value":30}`},
		{"float", "21.5", `{"app":"a","key":"k","value":21.5}`},
		{"bool", "true", `{"app":"a","key":"k","value":true}`},
		{"string", "eco", `{"app":"a","key":"k","value":"eco"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _ := store.Open(t.TempDir() + "/c.db")
			defer st.Close()
			st.EnsureNode("dev", 1000)
			var out bytes.Buffer
			if err := runDeviceSet(&out, st, "dev", "a", "k", c.value, 2000); err != nil {
				t.Fatal(err)
			}
			next, _ := st.NextUndelivered("dev")
			if next == nil || next.Args != c.wantArgs {
				t.Errorf("Args=%v, want %s", next, c.wantArgs)
			}
		})
	}
}
