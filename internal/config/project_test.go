package config

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func cmd(id int64, verb, args, issuedBy string, delivered sql.NullInt64) store.Command {
	return store.Command{ID: id, Verb: verb, Args: args, IssuedBy: issuedBy, DeliveredAt: delivered}
}

func deliveredTS(ts int64) sql.NullInt64 { return sql.NullInt64{Int64: ts, Valid: true} }
func pending() sql.NullInt64             { return sql.NullInt64{} }

func TestProjectDesiredLastWriteWins(t *testing.T) {
	log := []store.Command{
		cmd(1, "set", `{"app":"a","key":"k","value":1}`, "cli", deliveredTS(10)),
		cmd(2, "set", `{"app":"a","key":"k","value":2}`, "cli", deliveredTS(20)),
		cmd(3, "stop", `{"name":"x"}`, "cli", deliveredTS(30)), // ignored
		cmd(4, "set", `{"app":"b","key":"j","value":"eco"}`, "cli", pending()),
	}
	d := ProjectDesired(log)
	if v, ok := d["a"]["k"].(json.Number); !ok || v.String() != "2" {
		t.Errorf(`a.k = %v (%T), want json.Number "2"`, d["a"]["k"], d["a"]["k"])
	}
	if v := d["b"]["j"]; v != "eco" {
		t.Errorf(`b.j = %v, want "eco"`, v)
	}
}

func TestProjectDesiredForApp(t *testing.T) {
	log := []store.Command{
		cmd(1, "set", `{"app":"a","key":"k","value":1}`, "cli", deliveredTS(10)),
		cmd(2, "set", `{"app":"b","key":"j","value":2}`, "cli", deliveredTS(20)),
	}
	a := ProjectDesiredForApp(log, "a")
	if len(a) != 1 {
		t.Errorf("len(a)=%d, want 1", len(a))
	}
	if ProjectDesiredForApp(log, "missing") == nil {
		t.Error("ProjectDesiredForApp on missing app should return empty map, not nil")
	}
}

func TestMarker(t *testing.T) {
	cases := []struct {
		name                       string
		desiredPresent, obsPresent bool
		desired, observed          any
		want                       string
	}{
		{"converged ints", true, true, json.Number("30"), json.Number("30"), ""},
		{"drift", true, true, json.Number("30"), json.Number("25"), "(drift)"},
		{"pending", true, false, json.Number("30"), nil, "(pending)"},
		{"observed-only", false, true, nil, json.Number("30"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Marker(c.desired, c.observed, c.desiredPresent, c.obsPresent)
			if got != c.want {
				t.Errorf("Marker(...)=%q, want %q", got, c.want)
			}
		})
	}
}
