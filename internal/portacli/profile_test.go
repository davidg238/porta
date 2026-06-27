// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunProfileStartPrints(t *testing.T) {
	c, st := newClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)

	var out bytes.Buffer
	if err := runProfileStart(&out, c, "aabbccddeeff", "myapp", 30, false, "run1"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "aabbccddeeff: profile start myapp (command #") {
		t.Errorf("unexpected output: %q", got)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "profile" {
		t.Fatalf("profile start did not enqueue profile verb: %+v", cmd)
	}
}
