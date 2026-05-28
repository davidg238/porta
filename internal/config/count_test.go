package config

import (
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestReconcileCount(t *testing.T) {
	log := []store.Command{
		cmd(1, "set", `{"app":"a","key":"k","value":1}`, "cli", deliveredTS(10)),
		cmd(2, "set", `{"app":"a","key":"k","value":1}`, "gateway-reconcile", deliveredTS(20)),
		cmd(3, "set", `{"app":"a","key":"k","value":1}`, "gateway-reconcile", deliveredTS(30)),
		cmd(4, "set", `{"app":"a","key":"j","value":2}`, "gateway-reconcile", deliveredTS(40)),
		cmd(5, "stop", `{"name":"x"}`, "gateway-reconcile", deliveredTS(50)),       // wrong verb
		cmd(6, "set", `{"app":"b","key":"k","value":1}`, "gateway-reconcile", deliveredTS(60)), // wrong app
	}
	if got := ReconcileCount(log, "a", "k"); got != 2 {
		t.Errorf("a.k count = %d, want 2 (cli row and non-set/wrong-app rows excluded)", got)
	}
	if got := ReconcileCount(log, "a", "j"); got != 1 {
		t.Errorf("a.j count = %d, want 1", got)
	}
	if got := ReconcileCount(log, "a", "missing"); got != 0 {
		t.Errorf("a.missing count = %d, want 0", got)
	}
}
