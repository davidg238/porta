// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunProfilePollShowsSessionStatus(t *testing.T) {
	c, st := newClientServer(t)
	// Arm at t=1000 dur=30, node reported at t=2000 (since arming), no result.
	st.EnsureNode("aabbccddeeff", 1000)
	st.UpsertProfileSession("aabbccddeeff", "myapp", "run1", 30, 1000)
	st.InsertReport("aabbccddeeff", "{}", "", 2000)

	var out bytes.Buffer
	if err := runProfilePoll(&out, c, "aabbccddeeff", 0); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// Long after the deadline (real wall-clock now ≫ 2090) → stale.
	if !strings.Contains(got, "session:") || !strings.Contains(got, "myapp") {
		t.Errorf("poll missing session line: %q", got)
	}
	if !strings.Contains(got, "stale") {
		t.Errorf("poll should show stale status: %q", got)
	}
}

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
