// Copyright (c) 2026 Ekorau LLC

package control

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSetEnqueuesTypedCommand(t *testing.T) {
	st := newStore(t)
	if err := st.EnsureNode("n1", 100); err != nil {
		t.Fatal(err)
	}
	id, err := Set(st, "n1", "demo", "gain", int64(3), "cli", 100)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("want nonzero command id")
	}
	cmds, _ := st.CommandLog("n1")
	if len(cmds) != 1 || cmds[0].Verb != "set" {
		t.Fatalf("got %+v", cmds)
	}
	if cmds[0].IssuedBy != "cli" {
		t.Fatalf("issued_by = %q, want cli", cmds[0].IssuedBy)
	}
	if !strings.Contains(cmds[0].Args, `"value":3`) {
		t.Fatalf("args lost type: %s", cmds[0].Args)
	}
}

func TestInstallFromReaderComputesCRCAndSize(t *testing.T) {
	st := newStore(t)
	if err := st.EnsureNode("n1", 100); err != nil {
		t.Fatal(err)
	}
	img := []byte("hello-image-bytes")
	wantCRC := int64(command.CRC32(img))
	id, err := Install(st, "n1", "hello", bytes.NewReader(img),
		InstallOpts{IntervalS: 30, Lifecycle: "run-once", Runlevel: 3}, "cli", 100)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("want run command enqueued")
	}
	got, err := st.Payload(wantCRC)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(img) {
		t.Fatalf("payload not registered under crc %d", wantCRC)
	}
	cmds, err := st.CommandLog("n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Verb != "run" {
		t.Fatalf("got %+v", cmds)
	}
	if !strings.Contains(cmds[0].Args, `"crc":`) || !strings.Contains(cmds[0].Args, `"size":17`) {
		t.Fatalf("run args missing crc/size: %s", cmds[0].Args)
	}
}

func TestResolveNodeID(t *testing.T) {
	st := newStore(t)
	st.TouchNode("aabbccddeeff", "1.2.3.4", 100) // auto-names it
	n, _ := st.GetNode("aabbccddeeff")
	if got, _ := ResolveNodeID(st, "aabbccddeeff"); got != "aabbccddeeff" {
		t.Fatalf("MAC: got %q", got)
	}
	if got, _ := ResolveNodeID(st, n.Name); got != "aabbccddeeff" {
		t.Fatalf("name: got %q", got)
	}
	if _, err := ResolveNodeID(st, "nope"); err == nil {
		t.Fatal("want error for unknown name")
	}
}

func TestIsNodeID(t *testing.T) {
	// 12-hex ESP32 MAC and 16-hex EUI-64 are both node ids (PROTOCOL.md §1).
	if !IsNodeID("30aea41a6208") || !IsNodeID("aabbccddeeff1122") {
		t.Error("12- and 16-hex lowercase should be node ids")
	}
	if IsNodeID("jolly-pine") || IsNodeID("AABBCCDDEEFF1122") ||
		IsNodeID("30aea41a620") || IsNodeID("aabbccddeeff11223") {
		t.Error("non-hex, uppercase, 11-hex, and 17-hex should not be node ids")
	}
}

func TestProfileStartStoresLabelAndEnqueues(t *testing.T) {
	st := newStore(t)
	if err := st.EnsureNode("aabbccddeeff", 1000); err != nil {
		t.Fatal(err)
	}

	cid, err := Profile(st, "aabbccddeeff", "myapp", "start", 30, false, "before-fix", "test", 1000)
	if err != nil || cid == 0 {
		t.Fatalf("profile start: cid=%d err=%v", cid, err)
	}
	sess, err := st.GetProfileSession("aabbccddeeff")
	if err != nil || sess == nil || sess.Label != "before-fix" || sess.App != "myapp" {
		t.Fatalf("session not stored: %+v err=%v", sess, err)
	}
}
