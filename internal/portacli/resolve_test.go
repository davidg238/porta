package portacli

import (
	"testing"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

func TestResolveNodeID(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/r.db")
	defer st.Close()
	st.TouchNode("aabbccddeeff", "p", 1000) // auto-named

	if id, err := resolveNodeID(st, "aabbccddeeff"); err != nil || id != "aabbccddeeff" {
		t.Errorf("by mac: %q %v", id, err)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if id, err := resolveNodeID(st, n.Name); err != nil || id != "aabbccddeeff" {
		t.Errorf("by name: %q %v", id, err)
	}
	if _, err := resolveNodeID(st, "no-such-node"); err == nil {
		t.Error("unknown node should error")
	}
}

func TestIsMAC(t *testing.T) {
	if !control.IsMAC("30aea41a6208") {
		t.Error("12 lowercase hex should be a MAC")
	}
	if control.IsMAC("jolly-pine") || control.IsMAC("AABBCCDDEEFF") || control.IsMAC("30aea41a620") {
		t.Error("non-12-lowercase-hex should not be a MAC")
	}
}
