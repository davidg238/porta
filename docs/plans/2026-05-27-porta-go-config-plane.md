# porta Go Core — B2 Config Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the parked Toit gateway's config plane (the `set` verb, observed-config consumption, self-heal reconcile, `device set`/`device get` CLI) into the Go core at parity, with no schema change.

**Architecture:** A new `internal/config` package holds the pure algorithmic logic (scalar inference, value equality, desired-state projection, reconcile, re-issue counting). `internal/handler` calls into it after every successful report ingest; `internal/portacli` calls into it for the operator CLI. The package adds no DB dependencies — it operates on `[]store.Command` slices and parsed JSON maps.

**Tech Stack:** Go 1.21+, `database/sql`, `mattn/go-sqlite3` (already wired by B1), `encoding/json` (with `UseNumber()` for type-faithful scalar comparison), `spf13/cobra` (already wired).

**Branch:** `feat/porta-go-config-plane` (already cut; spec committed at `b7baa6c`).

**Spec:** `docs/specs/2026-05-27-porta-go-config-plane-design.md`

---

## File Structure

The plan implements these files (new = N, modified = M). The spec listed four files in `internal/config`; the plan splits `EqualScalars` into its own `equal.go` (≤30 LoC) to keep `project.go` and `reconcile.go` focused — same behavior, slightly cleaner unit boundaries.

| Path | Kind | Responsibility |
|---|---|---|
| `internal/config/infer.go` | N | `InferScalar(string) any` |
| `internal/config/infer_test.go` | N | |
| `internal/config/equal.go` | N | `EqualScalars(a, b any) bool` (false-drift guard) |
| `internal/config/equal_test.go` | N | |
| `internal/config/project.go` | N | `ProjectDesired`, `ProjectDesiredForApp`, `Marker` |
| `internal/config/project_test.go` | N | |
| `internal/config/reconcile.go` | N | `Reissue` type, `Reconcile` |
| `internal/config/reconcile_test.go` | N | |
| `internal/config/count.go` | N | `ReconcileCount` |
| `internal/config/count_test.go` | N | |
| `internal/command/command.go` | M | `+ Set` constructor |
| `internal/command/command_test.go` | M | `+ Set` tests |
| `internal/handler/handler.go` | M | reconcile hook in `Write`; injectable `log` for tests |
| `internal/handler/handler_test.go` | M | drift / pending / self-throttle / failure-isolation scenarios |
| `internal/portacli/mutate.go` | M | `+ runDeviceSet`, `+ newDeviceSetCmd` |
| `internal/portacli/inspect.go` | M | `+ runDeviceGet`, `+ newDeviceGetCmd`, wire into `newDeviceCmd()` |
| `internal/portacli/config_test.go` | N | `device set` + `device get` (single, multi, warning) |

Total: 10 new files, 5 modified files.

---

## Task 1: `InferScalar` — typed parsing of operator CLI input

**Files:**
- Create: `internal/config/infer.go`
- Test: `internal/config/infer_test.go`

- [ ] **Step 1.1: Write the failing test**

```go
// internal/config/infer_test.go
package config

import "testing"

func TestInferScalar(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"false", false},
		{"30", int64(30)},
		{"-7", int64(-7)},
		{"21.5", 21.5},
		{"-0.25", -0.25},
		{"eco", "eco"},
		{"", ""},
		{"+30", int64(30)},   // strconv.ParseInt accepts a leading +
		{"3e2", 300.0},        // exponent form parses as float
		{"007", "007"},        // leading-zero non-numeric-shaped → string (matches reference)
	}
	for _, c := range cases {
		got := InferScalar(c.in)
		if got != c.want {
			t.Errorf("InferScalar(%q) = %v (%T), want %v (%T)", c.in, got, got, c.want, c.want)
		}
	}
}
```

- [ ] **Step 1.2: Run the test, see it fail**

Run: `go test ./internal/config/ -run TestInferScalar`
Expected: `FAIL` with `undefined: InferScalar` (package doesn't exist yet).

- [ ] **Step 1.3: Implement `InferScalar`**

```go
// internal/config/infer.go
// Package config implements the porta gateway's per-app config plane:
// scalar inference, desired-vs-observed projection, and the self-heal
// reconcile algorithm. It is pure logic — no I/O, no globals.
package config

import (
	"regexp"
	"strconv"
)

// integerShaped matches strings that strconv.ParseInt accepts AND that look
// like an integer literal (no leading zeros except "0"/"-0"). The leading-zero
// exclusion keeps things like "007" rendering as a string, matching the
// reference impl's intent (preserve operator's literal intent for opaque ids).
var integerShaped = regexp.MustCompile(`^[+-]?(0|[1-9][0-9]*)$`)

// InferScalar parses an operator-supplied CLI string into a typed scalar:
// "true"/"false" → bool; integer-shaped → int64; float-shaped → float64;
// anything else → the original string. Matches the reference's infer-scalar
// in examples/toit-gateway/command.toit.
func InferScalar(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if integerShaped.MatchString(s) {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		// Re-check it didn't sneak through as integer-shaped (already handled).
		return f
	}
	return s
}
```

- [ ] **Step 1.4: Run the test, see it pass**

Run: `go test ./internal/config/ -run TestInferScalar -v`
Expected: `PASS` — all 11 cases.

- [ ] **Step 1.5: Commit**

```bash
git add internal/config/infer.go internal/config/infer_test.go
git commit -m "$(cat <<'EOF'
feat(porta): config — InferScalar for typed CLI input

New internal/config package; first function: parse operator CLI strings
into typed scalars (bool, int64, float64, string). Mirrors infer-scalar
from the Toit gateway reference (examples/toit-gateway/command.toit).

Leading-zero strings stay as strings so opaque ids like "007" survive
end-to-end.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `command.Set` constructor

**Files:**
- Modify: `internal/command/command.go` (append a constructor)
- Modify: `internal/command/command_test.go` (append a test)

- [ ] **Step 2.1: Write the failing test**

Append to `internal/command/command_test.go`:

```go
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
```

- [ ] **Step 2.2: Run the test, see it fail**

Run: `go test ./internal/command/ -run TestSet -v`
Expected: FAIL with `undefined: Set`.

- [ ] **Step 2.3: Implement `Set`**

Append to `internal/command/command.go` (after `SetPollInterval`):

```go
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
```

- [ ] **Step 2.4: Run all command tests**

Run: `go test ./internal/command/ -v`
Expected: PASS for new tests; all existing tests still pass.

- [ ] **Step 2.5: Commit**

```bash
git add internal/command/command.go internal/command/command_test.go
git commit -m "$(cat <<'EOF'
feat(porta): command — Set constructor for the config-plane verb

Builds {"verb":"set","app":...,"key":...,"value":...} with the scalar
preserved as its JSON-native type. Caller is responsible for scalar
inference (config.InferScalar); Set rejects anything other than
int64/float64/bool/string up front.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `EqualScalars` — the false-drift guard

**Files:**
- Create: `internal/config/equal.go`
- Test: `internal/config/equal_test.go`

- [ ] **Step 3.1: Write the failing test**

```go
// internal/config/equal_test.go
package config

import (
	"encoding/json"
	"testing"
)

// num returns a json.Number from a literal (mimics what UseNumber() yields).
func num(s string) json.Number { return json.Number(s) }

func TestEqualScalars(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"both int same", num("30"), num("30"), true},
		{"int vs float same value", num("30"), num("30.0"), true},
		{"float vs int same value", num("30.0"), num("30"), true},
		{"different ints", num("30"), num("31"), false},
		{"int vs float different", num("30"), num("30.5"), false},
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"bool mismatch", true, false, false},
		{"string equal", "eco", "eco", true},
		{"string differ", "eco", "heat", false},
		{"cross-type string vs num", "30", num("30"), false},
		{"cross-type bool vs num", true, num("1"), false},
		{"large int preserves precision", num("9007199254740993"), num("9007199254740993"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EqualScalars(c.a, c.b); got != c.want {
				t.Errorf("EqualScalars(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 3.2: Run the test, see it fail**

Run: `go test ./internal/config/ -run TestEqualScalars`
Expected: FAIL with `undefined: EqualScalars`.

- [ ] **Step 3.3: Implement `EqualScalars`**

```go
// internal/config/equal.go
package config

import "encoding/json"

// EqualScalars returns true iff a and b represent the same scalar config
// value, comparing across the JSON-decode boundary. This is the false-drift
// guard: desired comes from the operator's CLI (CLI-inferred Go scalar OR
// json.Number when round-tripped through args JSON); observed comes from the
// node's report (json.Number under UseNumber()). A naive == on any would
// treat int64(30) and float64(30) as unequal — spurious self-heal forever.
//
// Comparison rules:
//   - Two json.Numbers: equal iff their canonical text matches OR they parse
//     to the same float64. The text short-circuit avoids float64's 53-bit
//     precision loss on large int64 keys.
//   - bool/bool, string/string: direct ==.
//   - Anything else (mixed types) → false.
func EqualScalars(a, b any) bool {
	na, aok := a.(json.Number)
	nb, bok := b.(json.Number)
	if aok && bok {
		if na.String() == nb.String() {
			return true
		}
		af, errA := na.Float64()
		bf, errB := nb.Float64()
		return errA == nil && errB == nil && af == bf
	}
	switch av := a.(type) {
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	}
	return false
}
```

- [ ] **Step 3.4: Run the test, see it pass**

Run: `go test ./internal/config/ -run TestEqualScalars -v`
Expected: PASS — all 13 cases.

- [ ] **Step 3.5: Commit**

```bash
git add internal/config/equal.go internal/config/equal_test.go
git commit -m "$(cat <<'EOF'
feat(porta): config — EqualScalars (the false-drift guard)

The reconcile algorithm and `device get` both compare a CLI-inferred
desired value against a JSON-decoded observed value. encoding/json would
otherwise yield float64(30) for observed and int64(30) for desired —
spurious drift forever. EqualScalars compares json.Number values by
canonical text first (precision-safe for large int64s), then by float64;
bool/bool and string/string compare directly; anything else is unequal.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `ProjectDesired` + `Marker` — the read-side projection

**Files:**
- Create: `internal/config/project.go`
- Test: `internal/config/project_test.go`

- [ ] **Step 4.1: Write the failing test**

```go
// internal/config/project_test.go
package config

import (
	"database/sql"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func cmd(id int64, verb, args, issuedBy string, delivered sql.NullInt64) store.Command {
	return store.Command{ID: id, Verb: verb, Args: args, IssuedBy: issuedBy, DeliveredAt: delivered}
}

func delivered(ts int64) sql.NullInt64 { return sql.NullInt64{Int64: ts, Valid: true} }
func pending() sql.NullInt64           { return sql.NullInt64{} }

func TestProjectDesiredLastWriteWins(t *testing.T) {
	log := []store.Command{
		cmd(1, "set", `{"app":"a","key":"k","value":1}`, "cli", delivered(10)),
		cmd(2, "set", `{"app":"a","key":"k","value":2}`, "cli", delivered(20)),
		cmd(3, "stop", `{"name":"x"}`, "cli", delivered(30)), // ignored
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
		cmd(1, "set", `{"app":"a","key":"k","value":1}`, "cli", delivered(10)),
		cmd(2, "set", `{"app":"b","key":"j","value":2}`, "cli", delivered(20)),
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
```

Add the import at the top:

```go
import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/davidg238/porta/internal/store"
)
```

- [ ] **Step 4.2: Run the test, see it fail**

Run: `go test ./internal/config/ -run "TestProjectDesired|TestMarker"`
Expected: FAIL with `undefined: ProjectDesired` / `undefined: Marker`.

- [ ] **Step 4.3: Implement `project.go`**

```go
// internal/config/project.go
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
```

- [ ] **Step 4.4: Run the tests**

Run: `go test ./internal/config/ -v`
Expected: PASS for all tests in the package so far (Infer, EqualScalars, ProjectDesired, ProjectDesiredForApp, Marker).

- [ ] **Step 4.5: Commit**

```bash
git add internal/config/project.go internal/config/project_test.go
git commit -m "$(cat <<'EOF'
feat(porta): config — ProjectDesired/ForApp + Marker

ProjectDesired folds a node's command log into app→{key:value} desired
state via last-write-wins; non-set verbs are skipped. Args are decoded
with UseNumber() so numeric scalars stay as json.Number, keeping the
EqualScalars comparison type-faithful across the JSON-decode boundary.

Marker renders the desired-vs-observed status for one key:
- present-on-both + equal     → ""             (converged)
- present-on-both + unequal   → "(drift)"
- desired only                → "(pending)"
- observed-only / both absent → ""

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `Reconcile` — the self-heal algorithm

**Files:**
- Create: `internal/config/reconcile.go`
- Test: `internal/config/reconcile_test.go`

- [ ] **Step 5.1: Write the failing test**

```go
// internal/config/reconcile_test.go
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
		wantRe   int // number of re-issues
		wantArgs string // expected Args of the (first) re-issue, "" if wantRe==0
	}
	cases := []tc{
		{
			name: "converged — no re-issue",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", delivered(10)),
			},
			observed: `{"a":{"k":30}}`,
			wantRe:   0,
		},
		{
			name: "drift — re-issue with byte-identical args",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", delivered(10)),
			},
			observed: `{"a":{"k":25}}`,
			wantRe:   1,
			wantArgs: `{"app":"a","key":"k","value":30}`,
		},
		{
			name: "pending — observed missing the key, re-issue",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", delivered(10)),
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
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", delivered(10)),
			},
			observed: `{"a":{"k":30.0}}`,
			wantRe:   0,
		},
		{
			name: "false drift bool — converged",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"on","value":true}`, "cli", delivered(10)),
			},
			observed: `{"a":{"on":true}}`,
			wantRe:   0,
		},
		{
			name: "observed-only key — not re-issued (no unset)",
			log:  []store.Command{},
			observed: `{"a":{"orphan":1}}`,
			wantRe:   0,
		},
		{
			name: "two divergent keys — two re-issues",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", delivered(10)),
				cmd(2, "set", `{"app":"a","key":"j","value":"eco"}`, "cli", delivered(20)),
			},
			observed: `{"a":{"k":25,"j":"heat"}}`,
			wantRe:   2,
		},
		{
			name: "later set supersedes earlier — only latest reconciled",
			log: []store.Command{
				cmd(1, "set", `{"app":"a","key":"k","value":30}`, "cli", delivered(10)),
				cmd(2, "set", `{"app":"a","key":"k","value":40}`, "cli", delivered(20)),
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
```

- [ ] **Step 5.2: Run the test, see it fail**

Run: `go test ./internal/config/ -run TestReconcileEachBranch`
Expected: FAIL with `undefined: Reconcile`.

- [ ] **Step 5.3: Implement `reconcile.go`**

```go
// internal/config/reconcile.go
package config

import "github.com/davidg238/porta/internal/store"

// Reissue describes a set command the gateway must re-enqueue because the
// node's observed config diverged from desired. Args is the verbatim
// ArgsJSON of the source row — replaying it guarantees the wire bytes on
// retry are byte-identical to the original send (no chance of int↔float
// type drift between attempts).
type Reissue struct {
	Verb string // always "set"
	Args string // verbatim from the source row's ArgsJSON
	App  string // for logging
	Key  string // for logging
}

// Reconcile produces the list of set commands the gateway must re-enqueue
// after ingesting a report. The algorithm (parity with reference):
//
//  1. Walk cmds in order; for each set, record latest[app][key] = sourceRow
//     (last-write-wins).
//  2. For each (app, key) → row:
//     - If row.DeliveredAt is NULL → skip (in-flight; also the self-throttle:
//       a re-issued gateway-reconcile row is itself undelivered, so the next
//       report finds it pending and skips).
//     - Else if observed[app][key] present and EqualScalars(desired,observed)
//       → skip (converged).
//     - Else → re-issue with byte-identical Args.
//
// Observed-only keys (desired absent) are never iterated — B2 has no unset.
func Reconcile(cmds []store.Command, observedConfig map[string]map[string]any) []Reissue {
	latest := map[string]map[string]*store.Command{}
	for i := range cmds {
		c := &cmds[i]
		if c.Verb != "set" {
			continue
		}
		app, key, _, ok := decodeSetArgs(c.Args)
		if !ok {
			continue
		}
		if latest[app] == nil {
			latest[app] = map[string]*store.Command{}
		}
		latest[app][key] = c
	}
	var out []Reissue
	for app, keys := range latest {
		obsApp := observedConfig[app]
		for key, row := range keys {
			if !row.DeliveredAt.Valid {
				continue // in-flight / self-throttle
			}
			_, _, desired, _ := decodeSetArgs(row.Args)
			obs, obsPresent := obsApp[key]
			if obsPresent && EqualScalars(desired, obs) {
				continue // converged
			}
			out = append(out, Reissue{
				Verb: row.Verb,
				Args: row.Args,
				App:  app,
				Key:  key,
			})
		}
	}
	return out
}
```

- [ ] **Step 5.4: Run the test, see it pass**

Run: `go test ./internal/config/ -run TestReconcileEachBranch -v`
Expected: PASS — all 9 cases.

- [ ] **Step 5.5: Commit**

```bash
git add internal/config/reconcile.go internal/config/reconcile_test.go
git commit -m "$(cat <<'EOF'
feat(porta): config — Reconcile (self-heal algorithm) + Reissue type

The core of B2's self-heal: walks a node's command log to compute the
latest-set per (app,key), then yields a Reissue for each desired entry
that is delivered AND not equal to observed. In-flight rows (delivered_at
NULL) are skipped — this is also the self-throttle: a re-issued
gateway-reconcile row is itself undelivered, so the next report finds it
pending and skips, capping re-issues at one per failed report.

Reissue.Args is the verbatim ArgsJSON of the source row, guaranteeing the
wire bytes on retry are byte-identical to the original send.

Observed-only keys are not iterated — B2 has no unset verb (parity with
the reference impl).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `ReconcileCount` — the ≥2× drift counter

**Files:**
- Create: `internal/config/count.go`
- Test: `internal/config/count_test.go`

- [ ] **Step 6.1: Write the failing test**

```go
// internal/config/count_test.go
package config

import (
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestReconcileCount(t *testing.T) {
	log := []store.Command{
		cmd(1, "set", `{"app":"a","key":"k","value":1}`, "cli", delivered(10)),
		cmd(2, "set", `{"app":"a","key":"k","value":1}`, "gateway-reconcile", delivered(20)),
		cmd(3, "set", `{"app":"a","key":"k","value":1}`, "gateway-reconcile", delivered(30)),
		cmd(4, "set", `{"app":"a","key":"j","value":2}`, "gateway-reconcile", delivered(40)),
		cmd(5, "stop", `{"name":"x"}`, "gateway-reconcile", delivered(50)),       // wrong verb
		cmd(6, "set", `{"app":"b","key":"k","value":1}`, "gateway-reconcile", delivered(60)), // wrong app
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
```

- [ ] **Step 6.2: Run the test, see it fail**

Run: `go test ./internal/config/ -run TestReconcileCount`
Expected: FAIL with `undefined: ReconcileCount`.

- [ ] **Step 6.3: Implement `count.go`**

```go
// internal/config/count.go
package config

import "github.com/davidg238/porta/internal/store"

// ReconcileCount returns how many times the gateway re-issued a set for the
// given (app, key) under its self-heal policy. Counts only rows where
// verb=="set" AND issued_by=="gateway-reconcile" AND args match the target.
// `device get` uses this for the ≥2× warning footer:
//   ⚠ <app>.<key>: self-healed N× — node may be failing to apply
func ReconcileCount(cmds []store.Command, app, key string) int {
	n := 0
	for _, c := range cmds {
		if c.Verb != "set" || c.IssuedBy != "gateway-reconcile" {
			continue
		}
		a, k, _, ok := decodeSetArgs(c.Args)
		if !ok || a != app || k != key {
			continue
		}
		n++
	}
	return n
}
```

- [ ] **Step 6.4: Run the test, see it pass**

Run: `go test ./internal/config/ -v`
Expected: PASS — all `internal/config` tests green.

- [ ] **Step 6.5: Commit**

```bash
git add internal/config/count.go internal/config/count_test.go
git commit -m "$(cat <<'EOF'
feat(porta): config — ReconcileCount for the ≥2× drift warning

Counts rows where verb=='set' AND issued_by=='gateway-reconcile' AND
(args.app,args.key) match. `device get` uses it to print the warning
footer when a key is still divergent AND ReconcileCount >= 2.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Handler reconcile hook + integration tests

**Files:**
- Modify: `internal/handler/handler.go` (Handler grows `log` injection; `Write` runs `reconcileAfterReport`)
- Modify: `internal/handler/handler_test.go` (drift / pending / self-throttle / failure-isolation scenarios)

- [ ] **Step 7.1: Write the failing integration tests**

Append to `internal/handler/handler_test.go`:

```go
func TestWriteReconcileReissuesOnDrift(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	// Operator sets a.k=30; deliver it.
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	if err := st.MarkDelivered(cmdID, 1101); err != nil {
		t.Fatal(err)
	}
	// Node reports a.k=25 (drift).
	body := []byte(`{"apps":{},"config":{"a":{"k":25}},"health":{}}`)
	if err := h.Write("report?id=dev", "1.2.3.4:5000", body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A gateway-reconcile re-issue should now be the next undelivered.
	next, err := st.NextUndelivered("dev")
	if err != nil || next == nil {
		t.Fatalf("NextUndelivered: %v %v", next, err)
	}
	if next.IssuedBy != "gateway-reconcile" {
		t.Errorf("issued_by = %q, want gateway-reconcile", next.IssuedBy)
	}
	if next.Args != `{"app":"a","key":"k","value":30}` {
		t.Errorf("re-issue Args = %s, want byte-identical to source", next.Args)
	}
}

func TestWriteReconcileSelfThrottle(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	body := []byte(`{"apps":{},"config":{"a":{"k":25}},"health":{}}`)
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatal(err)
	}
	// Second drifted report — re-issue from the first one is still pending
	// (delivered_at NULL), so reconcile MUST NOT issue another.
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatal(err)
	}
	log, _ := st.CommandLog("dev")
	reissues := 0
	for _, c := range log {
		if c.IssuedBy == "gateway-reconcile" {
			reissues++
		}
	}
	if reissues != 1 {
		t.Errorf("got %d gateway-reconcile rows, want 1 (self-throttle)", reissues)
	}
}

func TestWriteReconcileSecondCycleAfterDelivery(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	body := []byte(`{"apps":{},"config":{"a":{"k":25}},"health":{}}`)
	// First report → 1 re-issue.
	h.Write("report?id=dev", "p:1", body)
	// Mark the re-issue delivered, simulating the node fetching it.
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 1 {
		t.Fatalf("expected 1 undelivered re-issue, got %d", len(un))
	}
	st.MarkDelivered(un[0].ID, 1200)
	// Second drifted report → second re-issue is allowed (in-flight guard cleared).
	h.Write("report?id=dev", "p:1", body)
	log, _ := st.CommandLog("dev")
	reissues := 0
	for _, c := range log {
		if c.IssuedBy == "gateway-reconcile" {
			reissues++
		}
	}
	if reissues != 2 {
		t.Errorf("got %d gateway-reconcile rows, want 2", reissues)
	}
}

func TestWriteReconcilePending(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	cmdID, _ := st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.MarkDelivered(cmdID, 1101)
	// Report says config has app a but not the key k (delivered but lost).
	body := []byte(`{"apps":{},"config":{"a":{}},"health":{}}`)
	h.Write("report?id=dev", "p:1", body)
	un, _ := st.UndeliveredCommands("dev")
	if len(un) != 1 || un[0].IssuedBy != "gateway-reconcile" {
		t.Fatalf("pending key not re-issued: %+v", un)
	}
}

func TestWriteSucceedsEvenWithMalformedConfig(t *testing.T) {
	h, st := newH(t)
	st.EnsureNode("dev", 1000)
	// config field is a string, not an object — reconcile must not fail the write.
	body := []byte(`{"apps":{},"config":"oops","health":{}}`)
	if err := h.Write("report?id=dev", "p:1", body); err != nil {
		t.Fatalf("Write should swallow reconcile errors, got %v", err)
	}
	// Report row was still committed.
	n, _ := st.GetNode("dev")
	if n == nil || n.ObservedState == "" {
		t.Error("observed_state should be set even when reconcile bails")
	}
}
```

- [ ] **Step 7.2: Run the tests, see them fail**

Run: `go test ./internal/handler/ -run TestWriteReconcile -v`
Expected: FAIL — `Write` doesn't reconcile yet (no re-issues enqueued).

- [ ] **Step 7.3: Modify `Handler` to inject reconcile**

In `internal/handler/handler.go`, add an import and a `log` field. Replace the top of the file:

```go
package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/davidg238/porta/internal/command"
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/tftp"
)

// Handler dispatches TFTP resources against the store.
type Handler struct {
	store *store.Store
	now   func() int64
	log   func(format string, args ...any) // injectable; defaults to log.Printf
}

// New creates a Handler. now supplies the current epoch seconds (injectable
// for tests).
func New(st *store.Store, now func() int64) *Handler {
	return &Handler{store: st, now: now, log: log.Printf}
}

// SetLog replaces the handler's log sink (used by tests; production code
// keeps the default log.Printf).
func (h *Handler) SetLog(fn func(format string, args ...any)) { h.log = fn }
```

Then replace the existing `Write` method with:

```go
// Write ingests a completed report body: persist {apps,config} as
// observed_state, append to the report log, then run the self-heal reconcile
// best-effort. Reconcile failure NEVER fails the report ingest.
func (h *Handler) Write(resource, peer string, data []byte) error {
	base, params := parseResource(resource)
	id := params["id"]
	if base != "report" || id == "" {
		return fmt.Errorf("access denied")
	}
	if err := h.store.TouchNode(id, peer, h.now()); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("report: bad json: %w", err)
	}
	field := func(k string) json.RawMessage {
		if v, ok := obj[k]; ok {
			return v
		}
		return json.RawMessage("{}")
	}
	observed := fmt.Sprintf(`{"apps":%s,"config":%s}`, field("apps"), field("config"))
	health := string(field("health"))
	if err := h.store.InsertReport(id, observed, health, h.now()); err != nil {
		return err
	}
	h.reconcileAfterReport(id, field("config"))
	return nil
}

// reconcileAfterReport is the post-report self-heal hook. Best-effort:
// every error path (panic, SQL, decode) is caught and logged; nothing
// propagates to the TFTP layer.
func (h *Handler) reconcileAfterReport(id string, configRaw json.RawMessage) {
	defer func() {
		if r := recover(); r != nil {
			h.log("porta: reconcile panic for %s: %v", id, r)
		}
	}()
	dec := json.NewDecoder(bytes.NewReader(configRaw))
	dec.UseNumber()
	var observed map[string]map[string]any
	if err := dec.Decode(&observed); err != nil {
		h.log("porta: reconcile decode error for %s: %v", id, err)
		return
	}
	cmds, err := h.store.CommandLog(id)
	if err != nil {
		h.log("porta: reconcile command-log error for %s: %v", id, err)
		return
	}
	for _, r := range config.Reconcile(cmds, observed) {
		if _, err := h.store.EnqueueCommand(id, r.Verb, r.Args, "gateway-reconcile", h.now()); err != nil {
			h.log("porta: reconcile enqueue error for %s %s.%s: %v", id, r.App, r.Key, err)
			continue
		}
		h.log("porta: reconcile re-issued %s.%s for %s (observed diverged)", r.App, r.Key, id)
	}
}
```

Note: the existing `command` import is still used by `readCommands`; keep it. Same with `strconv`, `strings`. New imports: `bytes`, `log`, and `internal/config`.

- [ ] **Step 7.4: Run the integration tests, see them pass**

Run: `go test ./internal/handler/ -v`
Expected: PASS — all existing tests still pass, all 5 new reconcile tests pass.

- [ ] **Step 7.5: Run the full Go test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: green across `internal/{command,config,handler,portacli,store,tftp}` and `cmd/porta`.

- [ ] **Step 7.6: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "$(cat <<'EOF'
feat(porta): handler — self-heal reconcile hook in report ingest

After InsertReport, Write now decodes the report's config (UseNumber, type-
faithful), reads the node's command log, and calls config.Reconcile to
re-enqueue any delivered-but-divergent set with issued_by="gateway-reconcile".
Failure path is best-effort: a defer/recover catches panics; every error
inside reconcileAfterReport is logged and swallowed. The TFTP Write always
succeeds for a parseable report regardless of reconcile outcome (parity
with examples/toit-gateway/handler.toit's catch --trace).

Handler.SetLog is the test-injection point for capturing log lines; default
sink is log.Printf.

Integration tests cover: drift, pending (key absent), self-throttle (one
re-issue per failed report), second-cycle re-issue after delivery, and
malformed-config swallow.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: `porta device set` CLI

**Files:**
- Modify: `internal/portacli/mutate.go` (add `runDeviceSet` and `newDeviceSetCmd`)
- Modify: `internal/portacli/inspect.go` (wire `newDeviceSetCmd` into `newDeviceCmd()`)
- Create: `internal/portacli/config_test.go` (start the file; tests for `device set`)

- [ ] **Step 8.1: Write the failing test**

```go
// internal/portacli/config_test.go
package portacli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/store"
)

func TestRunDeviceSetEnqueuesCli(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/c.db")
	defer st.Close()
	st.EnsureNode("aabbccddeeff", 1000)

	var out bytes.Buffer
	if err := runDeviceSet(&out, st, "aabbccddeeff", "sampler", "interval", "30", 2000); err != nil {
		t.Fatal(err)
	}
	c, _ := st.NextUndelivered("aabbccddeeff")
	if c == nil {
		t.Fatal("expected a command")
	}
	if c.Verb != "set" {
		t.Errorf("verb=%q, want set", c.Verb)
	}
	if c.IssuedBy != "cli" {
		t.Errorf("issued_by=%q, want cli", c.IssuedBy)
	}
	if c.Args != `{"app":"sampler","key":"interval","value":30}` {
		t.Errorf("args=%s, want int-shaped 30", c.Args)
	}
	if !strings.Contains(out.String(), "enqueued set sampler.interval=30") {
		t.Errorf("stdout=%q, want enqueue message", out.String())
	}
}

func TestRunDeviceSetTypeInference(t *testing.T) {
	cases := []struct {
		name, value, wantArgs string
	}{
		{"int", "30", `{"app":"a","key":"k","value":30}`},
		{"float", "21.5", `{"app":"a","key":"k","value":21.5}`},
		{"bool", "true", `{"app":"a","key":"k","value":true}`},
		{"string", "eco", `{"app":"a","key":"k","value":"eco"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _ := store.Open(t.TempDir() + "/c.db")
			defer st.Close()
			st.EnsureNode("dev", 1000)
			var out bytes.Buffer
			if err := runDeviceSet(&out, st, "dev", "a", "k", c.value, 2000); err != nil {
				t.Fatal(err)
			}
			next, _ := st.NextUndelivered("dev")
			if next == nil || next.Args != c.wantArgs {
				t.Errorf("Args=%v, want %s", next, c.wantArgs)
			}
		})
	}
}
```

- [ ] **Step 8.2: Run the test, see it fail**

Run: `go test ./internal/portacli/ -run TestRunDeviceSet`
Expected: FAIL with `undefined: runDeviceSet`.

- [ ] **Step 8.3: Implement `runDeviceSet` + `newDeviceSetCmd`**

Append to `internal/portacli/mutate.go`:

```go
import (
	// existing imports plus:
	"io"

	"github.com/davidg238/porta/internal/config"
)

// runDeviceSet is the testable core of `porta device set`: it infers the
// scalar type from the operator's string, enqueues a set command tagged
// issued_by="cli", and prints a confirmation line to out.
func runDeviceSet(out io.Writer, st *store.Store, id, app, key, valueStr string, now int64) error {
	value := config.InferScalar(valueStr)
	c, err := command.Set(app, key, value)
	if err != nil {
		return err
	}
	cmdID, err := st.EnqueueCommand(id, c.Verb, c.ArgsJSON, "cli", now)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: enqueued set %s.%s=%v (command #%d)\n", id, app, key, value, cmdID)
	return nil
}

func newDeviceSetCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "set <app> <key> <value>",
		Short: "Enqueue a per-app config write (set verb)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			if err := st.EnsureNode(id, nowSec()); err != nil {
				return err
			}
			return runDeviceSet(cmd.OutOrStdout(), st, id, args[0], args[1], args[2], nowSec())
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
```

Make sure to import `io` and `internal/config` in `mutate.go` (if Go doesn't already group them, add into the existing import block).

In `internal/portacli/inspect.go`, find `newDeviceCmd()` and add `newDeviceSetCmd()` to the `AddCommand` list:

```go
func newDeviceCmd() *cobra.Command {
	parent := &cobra.Command{Use: "device", Short: "Per-node operations"}
	parent.AddCommand(
		newDeviceShowCmd(),
		newDeviceSetCmd(),
		newDeviceSetPollIntervalCmd(),
		newDeviceSetMaxOfflineCmd(),
		newDeviceNameCmd(),
	)
	return parent
}
```

- [ ] **Step 8.4: Run the tests, see them pass**

Run: `go test ./internal/portacli/ -run TestRunDeviceSet -v`
Expected: PASS for both new tests; full portacli suite stays green.

- [ ] **Step 8.5: Build the binary to confirm the cobra wiring**

Run: `go build ./cmd/porta && ./porta device set --help`
Expected: usage line `set <app> <key> <value>` with the `-d/--device` flag listed.

- [ ] **Step 8.6: Commit**

```bash
git add internal/portacli/mutate.go internal/portacli/inspect.go internal/portacli/config_test.go
git commit -m "$(cat <<'EOF'
feat(porta): cli — device set <app> <key> <value>

Enqueues a set command tagged issued_by="cli" with the value type-inferred
from the operator's string (config.InferScalar). Wired as a sub-command of
`device` alongside set-poll-interval/set-max-offline/name. Output format
matches existing mutate.go idioms: "<id>: enqueued set <app>.<key>=<v>
(command #<n>)".

The runDeviceSet helper takes an io.Writer so tests capture stdout without
touching globals.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: `porta device get` CLI (single-key + multi-key + warning)

**Files:**
- Modify: `internal/portacli/inspect.go` (add `runDeviceGet`, `newDeviceGetCmd`, wire into `newDeviceCmd()`)
- Modify: `internal/portacli/config_test.go` (append `device get` tests)

- [ ] **Step 9.1: Write the failing tests**

Append to `internal/portacli/config_test.go`:

```go
func TestRunDeviceGetSingleKeyConverged(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	// Mark delivered + observed echo matches → converged.
	un, _ := st.NextUndelivered("dev")
	st.MarkDelivered(un.ID, 1101)
	st.InsertReport("dev", `{"apps":{},"config":{"a":{"k":30}}}`, `{}`, 1200)

	var out bytes.Buffer
	if err := runDeviceGet(&out, st, "dev", "a", "k"); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.String())
	want := "dev: a.k desired=30 observed=30"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRunDeviceGetSingleKeyDrift(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	un, _ := st.NextUndelivered("dev")
	st.MarkDelivered(un.ID, 1101)
	st.InsertReport("dev", `{"apps":{},"config":{"a":{"k":25}}}`, `{}`, 1200)

	var out bytes.Buffer
	runDeviceGet(&out, st, "dev", "a", "k")
	if !strings.Contains(out.String(), "desired=30 observed=25 (drift)") {
		t.Errorf("missing drift marker: %q", out.String())
	}
}

func TestRunDeviceGetSingleKeyPending(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	un, _ := st.NextUndelivered("dev")
	st.MarkDelivered(un.ID, 1101)
	// Observed has app a but no key k.
	st.InsertReport("dev", `{"apps":{},"config":{"a":{}}}`, `{}`, 1200)

	var out bytes.Buffer
	runDeviceGet(&out, st, "dev", "a", "k")
	if !strings.Contains(out.String(), "desired=30 observed=-- (pending)") {
		t.Errorf("missing pending marker: %q", out.String())
	}
}

func TestRunDeviceGetMultiKeyTable(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/g.db")
	defer st.Close()
	st.EnsureNode("dev", 1000)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"j","value":"eco"}`, "cli", 1101)
	for _, c := range mustCommands(t, st, "dev") {
		st.MarkDelivered(c.ID, 1102)
	}
	st.InsertReport("dev", `{"apps":{},"config":{"a":{"k":30,"j":"heat","z":1}}}`, `{}`, 1200)

	var out bytes.Buffer
	if err := runDeviceGet(&out, st, "dev", "a", ""); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	mustContain := []string{
		"config for a",
		"KEY", "DESIRED", "OBSERVED",
		"k", "30",                     // converged row
		"j", "eco", "heat", "(drift)", // drift row
		"z", "1",                       // observed-only (no marker)
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
	st.EnsureNode("dev", 1000)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "cli", 1100)
	// Two gateway-reconcile re-issues already in the log.
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "gateway-reconcile", 1200)
	st.EnqueueCommand("dev", "set", `{"app":"a","key":"k","value":30}`, "gateway-reconcile", 1300)
	// Mark the original cli row delivered; leave reconciles pending.
	un, _ := st.NextUndelivered("dev")
	st.MarkDelivered(un.ID, 1101)
	// Observed still wrong → warning should fire.
	st.InsertReport("dev", `{"apps":{},"config":{"a":{"k":25}}}`, `{}`, 1400)

	var out bytes.Buffer
	runDeviceGet(&out, st, "dev", "a", "k")
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
```

- [ ] **Step 9.2: Run the tests, see them fail**

Run: `go test ./internal/portacli/ -run TestRunDeviceGet`
Expected: FAIL with `undefined: runDeviceGet`.

- [ ] **Step 9.3: Implement `runDeviceGet` and wire it**

Append to `internal/portacli/inspect.go`:

```go
// (add these imports at the top of the file if not already present)
import (
	"bytes"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
)

// configFromObserved decodes a node's cached observed_state JSON into the
// app→{key:value} map for config display + comparison. Uses UseNumber() so
// values match the desired side under EqualScalars.
func configFromObserved(observed string) map[string]map[string]any {
	if observed == "" {
		return map[string]map[string]any{}
	}
	var obj struct {
		Config map[string]map[string]any `json:"config"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(observed)))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj.Config == nil {
		return map[string]map[string]any{}
	}
	return obj.Config
}

// renderScalar formats a scalar for the desired/observed cells. json.Number
// prints as its canonical text; bool/string print as-is.
func renderScalar(v any) string {
	if v == nil {
		return "--"
	}
	return fmt.Sprintf("%v", v)
}

// runDeviceGet is the testable core of `porta device get`. If key is empty,
// it renders a table over the union of desired ∪ observed keys for app;
// otherwise it renders the single-key one-liner. Either form prints a ≥2×
// self-heal warning footer for each still-divergent key.
func runDeviceGet(out io.Writer, st *store.Store, id, app, key string) error {
	n, err := st.GetNode(id)
	if err != nil || n == nil {
		return fmt.Errorf("node %s not found", id)
	}
	cmds, err := st.CommandLog(id)
	if err != nil {
		return err
	}
	desired := config.ProjectDesiredForApp(cmds, app)
	observed := configFromObserved(n.ObservedState)[app]
	if observed == nil {
		observed = map[string]any{}
	}

	if key != "" {
		d, dOK := desired[key]
		o, oOK := observed[key]
		marker := config.Marker(d, o, dOK, oOK)
		line := fmt.Sprintf("%s: %s.%s desired=%s observed=%s", id, app, key, renderScalar(d), renderScalar(o))
		if marker != "" {
			line += " " + marker
		}
		fmt.Fprintln(out, line)
		printWarnings(out, id, app, []string{key}, desired, observed, cmds)
		return nil
	}

	// Multi-key: union of desired ∪ observed, sorted.
	keys := unionKeys(desired, observed)
	fmt.Fprintf(out, "%s: config for %s\n", id, app)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  KEY\tDESIRED\tOBSERVED\t")
	for _, k := range keys {
		d, dOK := desired[k]
		o, oOK := observed[k]
		marker := config.Marker(d, o, dOK, oOK)
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", k, renderScalar(d), renderScalar(o), marker)
	}
	w.Flush()
	printWarnings(out, id, app, keys, desired, observed, cmds)
	return nil
}

func unionKeys(a, b map[string]any) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// printWarnings emits the ≥2× self-heal footer for each still-divergent key.
func printWarnings(out io.Writer, id, app string, keys []string, desired, observed map[string]any, cmds []store.Command) {
	for _, k := range keys {
		d, dOK := desired[k]
		o, oOK := observed[k]
		if config.Marker(d, o, dOK, oOK) == "" {
			continue
		}
		if n := config.ReconcileCount(cmds, app, k); n >= 2 {
			fmt.Fprintf(out, "%s: ⚠ %s.%s: self-healed %d× — node may be failing to apply\n", id, app, k, n)
		}
	}
}

func newDeviceGetCmd() *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "get <app> [key]",
		Short: "Show desired vs observed config for an app (or one key)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			id, err := resolveNodeID(st, device)
			if err != nil {
				return err
			}
			key := ""
			if len(args) == 2 {
				key = args[1]
			}
			return runDeviceGet(cmd.OutOrStdout(), st, id, args[0], key)
		},
	}
	deviceFlag(cmd, &device)
	return cmd
}
```

Then wire it into `newDeviceCmd()` in `inspect.go`:

```go
func newDeviceCmd() *cobra.Command {
	parent := &cobra.Command{Use: "device", Short: "Per-node operations"}
	parent.AddCommand(
		newDeviceShowCmd(),
		newDeviceGetCmd(),
		newDeviceSetCmd(),
		newDeviceSetPollIntervalCmd(),
		newDeviceSetMaxOfflineCmd(),
		newDeviceNameCmd(),
	)
	return parent
}
```

(Replace the existing `newDeviceCmd` body with this; it now includes both `get` and `set`.)

- [ ] **Step 9.4: Run the tests, see them pass**

Run: `go test ./internal/portacli/ -v`
Expected: PASS — five new `TestRunDeviceGet*` tests + everything else still green.

- [ ] **Step 9.5: Build + smoke-check the CLI**

Run: `go build ./cmd/porta && ./porta device get --help && ./porta device --help`
Expected: `get <app> [key]` shows in usage; the `device` parent lists `get` and `set` alongside the existing subcommands.

- [ ] **Step 9.6: Commit**

```bash
git add internal/portacli/inspect.go internal/portacli/config_test.go
git commit -m "$(cat <<'EOF'
feat(porta): cli — device get <app> [key]

Single-key form prints "<id>: <app>.<key> desired=<d> observed=<o> [marker]"
where marker is (drift)/(pending)/converged. Multi-key form prints a
KEY/DESIRED/OBSERVED table over the union of desired ∪ observed keys.
Both forms emit a "⚠ <app>.<key>: self-healed N× — node may be failing
to apply" footer for each still-divergent key with ReconcileCount >= 2.

Internal: configFromObserved decodes the node's cached observed_state
with UseNumber() so comparisons stay type-faithful (config.EqualScalars).
unionKeys + tabwriter handle multi-key rendering; renderScalar prints "--"
for absent values.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Acceptance gate

**Files:** none changed (verification only)

- [ ] **Step 10.1: Build, vet, race-test the Go core**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: all green across `cmd/porta`, `internal/{command,config,handler,portacli,store,tftp}`, and the parked `internal/st/...`. No new failures.

- [ ] **Step 10.2: Verify the reference Toit gateway still builds and passes host tests**

Run: `examples/toit-gateway/run-host-tests.sh`
Expected: existing Toit host suites green (the reference must remain at parity, not get superseded by B2).

- [ ] **Step 10.3: Manual local smoke (no hardware)**

```bash
# In one terminal:
rm -f /tmp/porta-b2.db
./porta --db /tmp/porta-b2.db serve --port 6970 &
SERVE=$!

# In another terminal — use the test address that won't reach a real node.
DEV=aabbccddeeff
./porta --db /tmp/porta-b2.db device set -d "$DEV" foo k 30
./porta --db /tmp/porta-b2.db log -d "$DEV"      # one set, pending
./porta --db /tmp/porta-b2.db device get -d "$DEV" foo k
# Expect: desired=30 observed=-- (pending) — because no node has reported yet
# and the cli row isn't yet delivered.

kill $SERVE
```
Expected:
- `device set` prints `aabbccddeeff: enqueued set foo.k=30 (command #1)`.
- `log` shows one `set` row, pending.
- `device get` shows `desired=30 observed=-- (pending)`.

- [ ] **Step 10.4: Hardware checkpoint (post-merge)**

Defer until after the branch merges; mirrors B1's checkpoint procedure (see [[porta-go-mainline-renovation]]):
1. Re-flash the spare ESP32 (`witty-jaguar`) with a fresh control-demo image.
2. From master, run `porta device set -d witty-jaguar control-demo interval 10` (or equivalent key/value the example actually reads).
3. Confirm the node fetches the set, applies it, and the next report shows the new value (use `porta device get -d witty-jaguar control-demo`).
4. Force a drift by uninstalling/reinstalling the example with a different default; verify the gateway-reconcile re-issue lands on the next wake and converges.
5. Sanity-check the ≥2× warning by forcing two failed cycles.

Note: don't run this from the working branch — merge first (matches the B1 cadence in memory).

- [ ] **Step 10.5: Update task tracker + memory after merge**

After hardware checkpoint passes:
1. Merge to master `--no-ff` with a B2 summary commit.
2. Delete the feature branch.
3. Update `MEMORY.md` / `porta-go-mainline-renovation.md` to record "B2 shipped @<hash>" and pivot the NEXT JOB to B3 (telemetry plane) — see the renovation memo for the agreed sequence.

---

## Self-Review (writing-plans checklist)

**Spec coverage:**
- `set` verb → Task 2 ✓
- per-app config storage → no schema change, already there; consumption in Tasks 4–9 ✓
- observed-config echo consumption → Task 7 (handler) and Task 9 (CLI) ✓
- self-heal reconcile (issued_by tagging, in-flight guard, ≥2× warning) → Tasks 5, 6, 7, 9 ✓
- `device set`/`device get` CLI → Tasks 8, 9 ✓
- false-drift guard (UseNumber + EqualScalars) → Task 3, applied in Tasks 4/5/9 ✓
- failure isolation (defer/recover + log injection) → Task 7 ✓
- testing strategy (unit + integration + CLI + acceptance gate) → Tasks 1–10 ✓
- explicit out-of-scope (unset, default-device, multi-key atomicity) → none implemented (correct)

**Placeholder scan:** no TBDs, no "implement later", every code step has its full code, every test step shows its assertions.

**Type consistency:**
- `Reissue` fields: `Verb`, `Args`, `App`, `Key` — used identically in Task 5 (definition), Task 7 (re-enqueue site).
- `config.InferScalar(string) any` — defined Task 1, used Task 8.
- `config.EqualScalars(a, b any) bool` — defined Task 3, used inside `Marker` (Task 4) and `Reconcile` (Task 5).
- `config.Marker(desired, observed any, desiredPresent, observedPresent bool) string` — defined Task 4, used Task 9 (single-key, multi-key, warnings).
- `config.ProjectDesired` / `ProjectDesiredForApp` — defined Task 4, used Task 9.
- `config.ReconcileCount(cmds, app, key) int` — defined Task 6, used Task 9.
- `Handler.SetLog(func(string, ...any))` — defined Task 7; tests can use it if/when a follow-up wants to assert log lines (not required by current tests since behavior is asserted on SQL state).

Plan is consistent and complete.
