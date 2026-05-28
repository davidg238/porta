package command

import (
	"encoding/json"
	"testing"
)

func TestRunDefaults(t *testing.T) {
	c, err := Run(RunSpec{Name: "blink", CRC: 7, Size: 4096, Triggers: map[string]int64{"interval": 30}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Verb != "run" {
		t.Fatalf("verb = %q", c.Verb)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(c.ArgsJSON), &m); err != nil {
		t.Fatal(err)
	}
	if m["runlevel"].(float64) != 3 {
		t.Errorf("default runlevel = %v, want 3", m["runlevel"])
	}
	if m["lifecycle"].(string) != "run-once" {
		t.Errorf("default lifecycle = %v, want run-once", m["lifecycle"])
	}
	if args, ok := m["arguments"].([]interface{}); !ok || len(args) != 0 {
		t.Errorf("default arguments = %v, want []", m["arguments"])
	}
}

func TestRunRejectsBadLifecycle(t *testing.T) {
	_, err := Run(RunSpec{Name: "x", CRC: 1, Size: 1, Triggers: map[string]int64{"boot": 1}, Lifecycle: "always"})
	if err == nil {
		t.Error("expected error for invalid lifecycle")
	}
}

func TestEncodeWireShapeFlat(t *testing.T) {
	wire := EncodeWire("run", `{"name":"blink","crc":7}`)
	var m map[string]interface{}
	if err := json.Unmarshal(wire, &m); err != nil {
		t.Fatal(err)
	}
	if m["verb"] != "run" || m["name"] != "blink" {
		t.Errorf("flat wire wrong: %v", m)
	}
	if _, nested := m["args"]; nested {
		t.Error("args must be flattened, not nested")
	}
}

func TestEncodeWireScalarFidelity(t *testing.T) {
	wire := EncodeWire("set", `{"temp":21.5,"count":7}`)
	s := string(wire)
	if !contains(s, "21.5") {
		t.Errorf("float 21.5 lost in %q", s)
	}
	if contains(s, "7.0") || !contains(s, `"count":7`) {
		t.Errorf("int 7 became float in %q", s)
	}
}

func TestSetPollIntervalAndStop(t *testing.T) {
	c := SetPollInterval(45)
	if c.Verb != "set-poll-interval" {
		t.Fatalf("verb = %q", c.Verb)
	}
	if c.ArgsJSON != `{"interval":45}` {
		t.Errorf("args = %q", c.ArgsJSON)
	}
	st := Stop("blink")
	if st.Verb != "stop" || st.ArgsJSON != `{"name":"blink"}` {
		t.Errorf("stop = %+v", st)
	}
}

func TestTriggersFromFlags(t *testing.T) {
	m, err := TriggersFromFlags([]string{"boot", "gpio-high=21", "install=1"}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if m["boot"] != 1 || m["interval"] != 60 || m["install"] != 1 || m["gpio-high:21"] != 21 {
		t.Errorf("triggers = %v", m)
	}
}

func TestTriggersRejectsUnknown(t *testing.T) {
	if _, err := TriggersFromFlags([]string{"laser=1"}, 0); err == nil {
		t.Error("unknown trigger should be rejected")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestSet(t *testing.T) {
	cases := []struct {
		name      string
		app, key  string
		value     any
		wantArgs  string
	}{
		{"int", "sampler", "interval", int64(30), `{"app":"sampler","key":"interval","value":30}`},
		{"float", "thermostat", "setpoint", 21.5, `{"app":"thermostat","key":"setpoint","value":21.5}`},
		{"bool", "x", "on", true, `{"app":"x","key":"on","value":true}`},
		{"string", "x", "mode", "eco", `{"app":"x","key":"mode","value":"eco"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, err := Set(c.app, c.key, c.value)
			if err != nil {
				t.Fatal(err)
			}
			if cmd.Verb != "set" {
				t.Errorf("verb = %q, want set", cmd.Verb)
			}
			if cmd.ArgsJSON != c.wantArgs {
				t.Errorf("ArgsJSON = %s, want %s", cmd.ArgsJSON, c.wantArgs)
			}
		})
	}
}

func TestSetRejectsBadType(t *testing.T) {
	if _, err := Set("a", "k", []int{1, 2}); err == nil {
		t.Error("Set with slice value should error")
	}
}

func TestSetConsole(t *testing.T) {
	on := SetConsole(true)
	if on.Verb != "set-console" {
		t.Errorf("Verb=%q, want set-console", on.Verb)
	}
	if on.ArgsJSON != `{"on":true}` {
		t.Errorf("ArgsJSON=%s, want {\"on\":true}", on.ArgsJSON)
	}
	off := SetConsole(false)
	if off.ArgsJSON != `{"on":false}` {
		t.Errorf("ArgsJSON=%s, want {\"on\":false}", off.ArgsJSON)
	}
	// Wire round-trip: verb + args spliced in flat form.
	wire := EncodeWire(on.Verb, on.ArgsJSON)
	verb, args, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if verb != "set-console" {
		t.Errorf("decoded verb=%q, want set-console", verb)
	}
	if v, ok := args["on"].(bool); !ok || !v {
		t.Errorf("decoded on=%v (%T), want bool true", args["on"], args["on"])
	}
}
