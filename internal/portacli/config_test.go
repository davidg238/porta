// Copyright (c) 2026 Ekorau LLC

package portacli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/devsdk/apiclient"
	"github.com/davidg238/porta/internal/store"
)

// getClient stands up a real apisrv over st and returns an apiclient pointed at
// it, so the device-get core exercises the same HTTP path the CLI uses.
func getClient(t *testing.T, st *store.Store) *apiclient.Client {
	t.Helper()
	_, url := serveStore(t, st)
	return apiclient.New(url)
}

func TestRunDeviceGetSingleKeyConverged(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	// Mark delivered + observed echo matches → converged.
	un, _ := st.NextUndelivered("aabbccddeeff")
	st.MarkDelivered(un.ID, 1101)
	st.InsertReport("aabbccddeeff", `{"apps":{},"config":{"a":{"k":30}}}`, `{}`, 1200)

	var out bytes.Buffer
	if err := runDeviceGet(&out, getClient(t, st), "aabbccddeeff", "a", "k"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.String())
	want := "aabbccddeeff: a.k desired=30 observed=30"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRunDeviceGetSingleKeyDrift(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	un, _ := st.NextUndelivered("aabbccddeeff")
	st.MarkDelivered(un.ID, 1101)
	st.InsertReport("aabbccddeeff", `{"apps":{},"config":{"a":{"k":25}}}`, `{}`, 1200)

	var out bytes.Buffer
	runDeviceGet(&out, getClient(t, st), "aabbccddeeff", "a", "k")
	if !strings.Contains(out.String(), "desired=30 observed=25 (drift)") {
		t.Errorf("missing drift marker: %q", out.String())
	}
}

func TestRunDeviceGetSingleKeyPending(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	un, _ := st.NextUndelivered("aabbccddeeff")
	st.MarkDelivered(un.ID, 1101)
	// Observed has app a but no key k.
	st.InsertReport("aabbccddeeff", `{"apps":{},"config":{"a":{}}}`, `{}`, 1200)

	var out bytes.Buffer
	runDeviceGet(&out, getClient(t, st), "aabbccddeeff", "a", "k")
	if !strings.Contains(out.String(), "desired=30 observed=-- (pending)") {
		t.Errorf("missing pending marker: %q", out.String())
	}
}

func TestRunDeviceGetMultiKeyTable(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"j","value":"eco"}`, "cli", 1101)
	for _, c := range mustCommands(t, st, "aabbccddeeff") {
		st.MarkDelivered(c.ID, 1102)
	}
	st.InsertReport("aabbccddeeff", `{"apps":{},"config":{"a":{"k":30,"j":"heat","z":1}}}`, `{}`, 1200)

	var out bytes.Buffer
	if err := runDeviceGet(&out, getClient(t, st), "aabbccddeeff", "a", ""); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	mustContain := []string{
		"config for a",
		"KEY", "DESIRED", "OBSERVED",
		"k", "30", // converged row
		"j", "eco", "heat", "(drift)", // drift row
		"z", "1", // observed-only (no marker)
	}
	for _, w := range mustContain {
		if !strings.Contains(s, w) {
			t.Errorf("table output missing %q; got:\n%s", w, s)
		}
	}
}

func TestRunDeviceGetWarningAtTwoOrMore(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	// Two gateway-reconcile re-issues already in the log.
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "gateway-reconcile", 1200)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"a","key":"k","value":30}`, "gateway-reconcile", 1300)
	// Mark the original cli row delivered; leave reconciles pending.
	un, _ := st.NextUndelivered("aabbccddeeff")
	st.MarkDelivered(un.ID, 1101)
	// Observed still wrong → warning should fire.
	st.InsertReport("aabbccddeeff", `{"apps":{},"config":{"a":{"k":25}}}`, `{}`, 1400)

	var out bytes.Buffer
	runDeviceGet(&out, getClient(t, st), "aabbccddeeff", "a", "k")
	if !strings.Contains(out.String(), "⚠ a.k: self-healed 2×") {
		t.Errorf("missing warning: %q", out.String())
	}
}

// mustCommands fetches the device's command log, failing the test on error.
func mustCommands(t *testing.T, st *store.Store, id string) []store.Command {
	t.Helper()
	cs, err := st.CommandLog(id)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}
