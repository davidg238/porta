// Copyright (c) 2026 Ekorau LLC

package config

import (
	"bytes"
	"encoding/json"

	"github.com/davidg238/porta/internal/store"
)

// ProjectDesired folds a node's full command log into desired config state:
// app → {key: value}. Only set verbs contribute; later sets overwrite
// earlier ones (last-write-wins). Decoded scalars are one of json.Number,
// bool, or string — never float64 (we use json.Decoder.UseNumber() so the
// downstream EqualScalars comparison stays type-faithful).
func ProjectDesired(cmds []store.Command) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, c := range cmds {
		if c.Verb != "set" {
			continue
		}
		app, key, value, ok := decodeSetArgs(c.Args)
		if !ok {
			continue
		}
		if out[app] == nil {
			out[app] = map[string]any{}
		}
		out[app][key] = value
	}
	return out
}

// ProjectDesiredForApp is like ProjectDesired but returns just one app's
// map (never nil — empty when the app has no set commands).
func ProjectDesiredForApp(cmds []store.Command, app string) map[string]any {
	full := ProjectDesired(cmds)
	if m := full[app]; m != nil {
		return m
	}
	return map[string]any{}
}

// Marker renders the desired-vs-observed status for one key. Caller indicates
// presence on each side; values may be any of the JSON-decoded scalar types
// (compared via EqualScalars). Returns "(drift)", "(pending)", or "".
//
// Truth table:
//   desired present, observed present, equal       → ""
//   desired present, observed present, !equal      → "(drift)"
//   desired present, observed absent               → "(pending)"
//   desired absent,  observed present              → ""  (observed-only; no unset)
//   desired absent,  observed absent               → ""  (empty)
func Marker(desired, observed any, desiredPresent, observedPresent bool) string {
	if desiredPresent && observedPresent {
		if EqualScalars(desired, observed) {
			return ""
		}
		return "(drift)"
	}
	if desiredPresent && !observedPresent {
		return "(pending)"
	}
	return ""
}

// decodeSetArgs pulls (app, key, value) from a stored set command's ArgsJSON,
// using UseNumber() so numeric scalars come out as json.Number (preserving
// the original wire form for EqualScalars).
func decodeSetArgs(argsJSON string) (app, key string, value any, ok bool) {
	dec := json.NewDecoder(bytes.NewReader([]byte(argsJSON)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return "", "", nil, false
	}
	a, aok := m["app"].(string)
	k, kok := m["key"].(string)
	if !aok || !kok {
		return "", "", nil, false
	}
	return a, k, m["value"], true
}

// DecodeSetArgs exposes a single set command's (app, key, value) to callers
// outside this package — e.g. command-lifecycle convergence checks. Numeric
// values come out as json.Number (UseNumber), matching ConfigFromObserved so
// EqualScalars compares them faithfully.
func DecodeSetArgs(argsJSON string) (app, key string, value any, ok bool) {
	return decodeSetArgs(argsJSON)
}
