// Copyright (c) 2026 Ekorau LLC

package command

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Command is a control-plane command: a verb plus its args as a JSON object
// string (stored verbatim to preserve scalar types end to end).
type Command struct {
	Verb     string
	ArgsJSON string
}

// RunSpec describes a run command before encoding.
type RunSpec struct {
	Name      string
	CRC       int64
	Size      int64
	Triggers  map[string]int64
	Runlevel  int    // 0 means "use default 3"
	Lifecycle string // "" means "use default run-once"
	Arguments []string
}

func validLifecycle(lc string) bool { return lc == "run-once" || lc == "run-loop" }

// Run builds a run command, applying defaults (runlevel 3, lifecycle run-once,
// empty arguments) and validating the lifecycle.
func Run(spec RunSpec) (Command, error) {
	runlevel := spec.Runlevel
	if runlevel == 0 {
		runlevel = 3
	}
	lifecycle := spec.Lifecycle
	if lifecycle == "" {
		lifecycle = "run-once"
	}
	if !validLifecycle(lifecycle) {
		return Command{}, fmt.Errorf("invalid lifecycle %q (expected run-once or run-loop)", lifecycle)
	}
	args := spec.Arguments
	if args == nil {
		args = []string{}
	}
	triggers := spec.Triggers
	if triggers == nil {
		triggers = map[string]int64{}
	}
	obj := map[string]interface{}{
		"name":      spec.Name,
		"crc":       spec.CRC,
		"size":      spec.Size,
		"triggers":  triggers,
		"runlevel":  runlevel,
		"lifecycle": lifecycle,
		"arguments": args,
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return Command{}, err
	}
	return Command{Verb: "run", ArgsJSON: string(b)}, nil
}

// Stop builds a stop command.
func Stop(name string) Command {
	b, _ := json.Marshal(map[string]string{"name": name})
	return Command{Verb: "stop", ArgsJSON: string(b)}
}

// Reboot builds a reboot command. It carries no args: the verb alone is the
// instruction. Unlike the declarative verbs it is imperative (one-shot), made
// redelivery-safe by the queue's deliver-once semantics rather than by being
// idempotent — see PROTOCOL.md §2.8.
func Reboot() Command {
	return Command{Verb: "reboot", ArgsJSON: `{}`}
}

// asInt coerces a wire/CLI value to int64, tolerating int64 (CLI), json.Number
// (API UseNumber decode), and float64 (plain JSON decode). Reports ok=false for
// a non-numeric or absent value.
func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		return int64(x), true
	case json.Number:
		i, err := x.Int64()
		return i, err == nil
	}
	return 0, false
}

// SetMode builds an atomic set-mode command from a verb-agnostic args map. Power
// mode is one declaration, so the whole command is accepted or rejected — porta
// validates client-side (the node re-validates authoritatively) mirroring
// nodus' validate-set-mode: always-on takes an optional loop_sleep_s (1..600);
// deep-sleep requires positive max_awake_s + max_asleep_s with an optional
// min_awake_s ≤ max_awake_s.
func SetMode(args map[string]any) (Command, error) {
	mode, _ := args["mode"].(string)
	switch mode {
	case "always-on":
		out := map[string]any{"mode": "always-on"}
		if lv, present := args["loop_sleep_s"]; present && lv != nil {
			loopSleep, ok := asInt(lv)
			if !ok || loopSleep <= 0 || loopSleep > 600 {
				return Command{}, fmt.Errorf("set-mode loop_sleep_s must be an int in 1..600")
			}
			out["loop_sleep_s"] = loopSleep
		}
		b, err := json.Marshal(out)
		if err != nil {
			return Command{}, err
		}
		return Command{Verb: "set-mode", ArgsJSON: string(b)}, nil
	case "deep-sleep":
		maxAwake, ok := asInt(args["max_awake_s"])
		if !ok || maxAwake <= 0 {
			return Command{}, fmt.Errorf("set-mode deep-sleep requires a positive max_awake_s")
		}
		maxAsleep, ok := asInt(args["max_asleep_s"])
		if !ok || maxAsleep <= 0 {
			return Command{}, fmt.Errorf("set-mode deep-sleep requires a positive max_asleep_s")
		}
		out := map[string]any{"mode": "deep-sleep", "max_awake_s": maxAwake, "max_asleep_s": maxAsleep}
		if mv, present := args["min_awake_s"]; present && mv != nil {
			minAwake, ok := asInt(mv)
			if !ok || minAwake <= 0 {
				return Command{}, fmt.Errorf("set-mode min_awake_s must be a positive int")
			}
			if minAwake > maxAwake {
				return Command{}, fmt.Errorf("set-mode min_awake_s (%d) must be <= max_awake_s (%d)", minAwake, maxAwake)
			}
			out["min_awake_s"] = minAwake
		}
		b, err := json.Marshal(out)
		if err != nil {
			return Command{}, err
		}
		return Command{Verb: "set-mode", ArgsJSON: string(b)}, nil
	default:
		return Command{}, fmt.Errorf("invalid mode %q (expected deep-sleep or always-on)", mode)
	}
}

// SetName builds a set-name command. The name is node-owned (stored in NVS and
// echoed back); porta only relays + mirrors it.
func SetName(name string) (Command, error) {
	if name == "" {
		return Command{}, fmt.Errorf("set-name requires a non-empty name")
	}
	nb, _ := json.Marshal(name)
	return Command{Verb: "set-name", ArgsJSON: fmt.Sprintf(`{"name":%s}`, nb)}, nil
}

// StreamPolicy is one northbound stream's forwarding policy. On is always
// emitted (explicit on/off). Level applies to the log stream only. EveryS is
// the reserved always-on per-stream cadence (no CLI surface yet — omitted when 0).
type StreamPolicy struct {
	On     bool   `json:"on"`
	Level  string `json:"level,omitempty"`
	EveryS int64  `json:"every_s,omitempty"`
}

// ForwardPolicy is the complete per-stream forwarding policy carried by
// set-forward. Absent streams are omitted from the wire; the node resolves an
// omitted stream to its default (off) — set-forward is absolute, not a patch.
type ForwardPolicy struct {
	Print     *StreamPolicy `json:"print,omitempty"`
	Log       *StreamPolicy `json:"log,omitempty"`
	Telemetry *StreamPolicy `json:"telemetry,omitempty"`
}

func validLogLevel(l string) bool {
	switch l {
	case "trace", "debug", "info", "warn", "error", "fatal":
		return true
	}
	return false
}

// SetForward builds a set-forward command from a complete forwarding policy.
// The optional log level is validated against the 6-term vocab; nested policy
// objects are spliced verbatim by EncodeWire so they reach the node intact.
func SetForward(p ForwardPolicy) (Command, error) {
	if p.Log != nil && p.Log.Level != "" && !validLogLevel(p.Log.Level) {
		return Command{}, fmt.Errorf("invalid log level %q (expected trace|debug|info|warn|error|fatal)", p.Log.Level)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return Command{}, err
	}
	return Command{Verb: "set-forward", ArgsJSON: string(b)}, nil
}

// Set builds a set command for one (app, key, scalar value). value must be
// one of int64, float64, bool, or string — the four scalar kinds InferScalar
// produces. Marshalled args are stable-ordered (app, key, value) so tests
// can compare the literal JSON string.
func Set(app, key string, value any) (Command, error) {
	switch value.(type) {
	case int64, float64, bool, string:
	default:
		return Command{}, fmt.Errorf("set: unsupported value type %T (want int64, float64, bool, or string)", value)
	}
	// Build by hand to guarantee key order — encoding/json on map sorts keys
	// alphabetically (app, key, value) which is what we want, but spelling
	// it out makes the wire shape obvious.
	vb, err := json.Marshal(value)
	if err != nil {
		return Command{}, err
	}
	ab, _ := json.Marshal(app)
	kb, _ := json.Marshal(key)
	args := fmt.Sprintf(`{"app":%s,"key":%s,"value":%s}`, ab, kb, vb)
	return Command{Verb: "set", ArgsJSON: args}, nil
}

// EncodeWire produces the flat wire JSON {"verb":<verb>, <args flattened>}.
// The args object is spliced in via json.RawMessage so number tokens are
// copied byte-for-byte — int stays int, float stays float.
func EncodeWire(verb, argsJSON string) []byte {
	fields := map[string]json.RawMessage{}
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &fields)
	}
	vb, _ := json.Marshal(verb)
	fields["verb"] = vb
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(fields[k])
	}
	sb.WriteByte('}')
	return []byte(sb.String())
}

// TriggersFromFlags parses --trigger specs plus an optional --interval shorthand
// into the trigger map. Valid keys: boot, interval=<s>, install=<n>,
// gpio-high=<pin>, gpio-low=<pin>, gpio-touch=<pin>. intervalS<=0 is ignored.
func TriggersFromFlags(flags []string, intervalS int64) (map[string]int64, error) {
	m := map[string]int64{}
	if intervalS > 0 {
		m["interval"] = intervalS
	}
	for _, spec := range flags {
		eq := strings.Index(spec, "=")
		if eq < 0 {
			if spec == "boot" {
				m["boot"] = 1
				continue
			}
			return nil, fmt.Errorf("unknown trigger: %s", spec)
		}
		typ := spec[:eq]
		val, err := strconv.ParseInt(spec[eq+1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid trigger value: %s", spec)
		}
		switch typ {
		case "interval", "install":
			m[typ] = val
		case "gpio-high", "gpio-low", "gpio-touch":
			m[fmt.Sprintf("%s:%d", typ, val)] = val
		default:
			return nil, fmt.Errorf("unknown trigger: %s", typ)
		}
	}
	return m, nil
}

// Decode parses a flat wire command back into verb + a typed args map, using
// json.Number so scalar types survive the round-trip (used by display/tests).
func Decode(wire []byte) (verb string, args map[string]interface{}, err error) {
	dec := json.NewDecoder(strings.NewReader(string(wire)))
	dec.UseNumber()
	raw := map[string]interface{}{}
	if err = dec.Decode(&raw); err != nil {
		return "", nil, err
	}
	if v, ok := raw["verb"].(string); ok {
		verb = v
	}
	args = map[string]interface{}{}
	for k, v := range raw {
		if k != "verb" {
			args[k] = v
		}
	}
	return verb, args, nil
}
