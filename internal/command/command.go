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

// SetPollInterval builds a set-poll-interval command.
func SetPollInterval(intervalS int64) Command {
	return Command{Verb: "set-poll-interval", ArgsJSON: fmt.Sprintf(`{"interval":%d}`, intervalS)}
}

// SetConsole builds the telemetry-forwarding toggle command.
func SetConsole(on bool) Command {
	if on {
		return Command{Verb: "set-console", ArgsJSON: `{"on":true}`}
	}
	return Command{Verb: "set-console", ArgsJSON: `{"on":false}`}
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
