package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

// decodeConfig parses an observed config JSON string the same way the handler
// will (UseNumber). Test helper.
func decodeConfig(t *testing.T, s string) map[string]map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw map[string]map[string]any
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("decodeConfig: %v", err)
	}
	return raw
}

func TestReconcileEachBranch(t *testing.T) {
	type tc struct {
		name     string
		log      []store.Command
		observed string
		wantRe   int    // number of re-issues
		wantArgs string // expected Args of the (first) re-issue, "" if wantRe==0
	}
	cases := []tc{
		{
			name: "converged — no re-issue",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", deliveredTS(10)),
			},
			observed: `{"a":{"k":30}}`,
			wantRe:   0,
		},
		{
			name: "drift — re-issue with byte-identical args",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", deliveredTS(10)),
			},
			observed: `{"a":{"k":25}}`,
			wantRe:   1,
			wantArgs: `{"app":"a","key":"k","value":30}`,
		},
		{
			name: "pending — observed missing the key, re-issue",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", deliveredTS(10)),
			},
			observed: `{"a":{}}`,
			wantRe:   1,
			wantArgs: `{"app":"a","key":"k","value":30}`,
		},
		{
			name: "in-flight — undelivered, skip (self-throttle)",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", pending()),
			},
			observed: `{"a":{"k":25}}`,
			wantRe:   0,
		},
		{
			name: "false drift int/float — converged",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", deliveredTS(10)),
			},
			observed: `{"a":{"k":30.0}}`,
			wantRe:   0,
		},
		{
			name: "false drift bool — converged",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"on","value":true}`, "cli", deliveredTS(10)),
			},
			observed: `{"a":{"on":true}}`,
			wantRe:   0,
		},
		{
			name:     "observed-only key — not re-issued (no unset)",
			log:      []store.Command{},
			observed: `{"a":{"orphan":1}}`,
			wantRe:   0,
		},
		{
			name: "two divergent keys — two re-issues",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", deliveredTS(10)),
				cmd(2, "set", `{"app":"a","key":"j","value":"eco"}`, "cli", deliveredTS(20)),
			},
			observed: `{"a":{"k":25,"j":"heat"}}`,
			wantRe:   2,
		},
		{
			name: "later set supersedes earlier — only latest reconciled",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", deliveredTS(10)),
				cmd(2, "set", `{"app":"a","key":"k","value":40}`, "cli", deliveredTS(20)),
			},
			observed: `{"a":{"k":25}}`,
			wantRe:   1,
			wantArgs: `{"app":"a","key":"k","value":40}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			obs := decodeConfig(t, c.observed)
			out := Reconcile(c.log, obs)
			if len(out) != c.wantRe {
				t.Fatalf("len(out)=%d, want %d (out=%+v)", len(out), c.wantRe, out)
			}
			if c.wantRe > 0 && c.wantArgs != "" && out[0].Args != c.wantArgs {
				t.Errorf("first re-issue Args = %s, want %s (must be byte-identical to source row)", out[0].Args, c.wantArgs)
			}
			if c.wantRe > 0 && out[0].Verb != "set" {
				t.Errorf("re-issue Verb = %q, want set", out[0].Verb)
			}
		})
	}
}
