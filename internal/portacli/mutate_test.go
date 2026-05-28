package portacli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/store"
)

func TestInstallRegistersPayloadAndEnqueuesRun(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir + "/m.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)

	bin := filepath.Join(dir, "blink.bin")
	img := []byte("fake-image-bytes")
	if err := os.WriteFile(bin, img, 0o644); err != nil {
		t.Fatal(err)
	}
	wantCRC := int64(command.CRC32(img))

	if err := runInstall(st, "aabbccddeeff", "blink", bin, installOpts{
		Lifecycle: "run-loop", Runlevel: 3, Triggers: []string{"boot"}, IntervalS: 0,
	}, 1000); err != nil {
		t.Fatal(err)
	}

	got, _ := st.Payload(wantCRC)
	if string(got) != string(img) {
		t.Errorf("payload not registered under crc %d", wantCRC)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil || c.Verb != "run" {
		t.Fatalf("expected run command, got %+v", c)
	}
	var args map[string]interface{}
	json.Unmarshal([]byte(c.Args), &args)
	if args["crc"].(float64) != float64(wantCRC) {
		t.Errorf("crc arg = %v, want %d", args["crc"], wantCRC)
	}
	if args["size"].(float64) != float64(len(img)) {
		t.Errorf("size arg = %v, want %d", args["size"], len(img))
	}
	if args["lifecycle"].(string) != "run-loop" {
		t.Errorf("lifecycle = %v", args["lifecycle"])
	}
	trig := args["triggers"].(map[string]interface{})
	if trig["boot"].(float64) != 1 {
		t.Errorf("triggers = %v", trig)
	}
}

func TestInstallRejectsNonBin(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir + "/m.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	pod := filepath.Join(dir, "x.pod")
	os.WriteFile(pod, []byte("x"), 0o644)
	if err := runInstall(st, "aabbccddeeff", "x", pod, installOpts{Lifecycle: "run-once"}, 1000); err == nil {
		t.Error(".pod must be rejected in B1 (only .bin)")
	}
}

func TestUninstallEnqueuesStop(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/u.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	if err := runUninstall(st, "aabbccddeeff", "blink", 1000); err != nil {
		t.Fatal(err)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil || c.Verb != "stop" || c.Args != `{"name":"blink"}` {
		t.Errorf("stop command wrong: %+v", c)
	}
}

func TestSetPollIntervalCachesAndEnqueues(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/p.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	if err := runSetPollInterval(st, "aabbccddeeff", 45, 1000); err != nil {
		t.Fatal(err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n.PollIntervalS != 45 {
		t.Errorf("poll_interval not cached: %d", n.PollIntervalS)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil || c.Verb != "set-poll-interval" {
		t.Errorf("expected set-poll-interval, got %+v", c)
	}
}
