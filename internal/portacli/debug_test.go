// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"testing"
)

func TestDebugAttachAndSend(t *testing.T) {
	c, st := newClientServer(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4:5", 1000)

	var out bytes.Buffer
	if err := runDebugAttach(&out, c, "aabbccddeeff", "blink"); err != nil {
		t.Fatal(err)
	}
	cmd, _ := st.NextUndelivered("aabbccddeeff")
	if cmd == nil || cmd.Verb != "debug" {
		t.Fatalf("attach did not enqueue debug verb: %+v", cmd)
	}
	if err := runDebugSend(&out, c, "aabbccddeeff", "dbg:methods"); err != nil {
		t.Fatal(err)
	}
	r, _ := st.NextUndeliveredDebugRequest("aabbccddeeff")
	if r == nil || r.Line != "dbg:methods" {
		t.Fatalf("send did not enqueue request: %+v", r)
	}
}
