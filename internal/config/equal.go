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
//   - Two json.Numbers: equal if canonical text matches; else if BOTH parse as
//     int64 (exact, no rounding) compare as int64; else compare as float64.
//     The int64-first fallback is what makes the function precision-safe at
//     the 2^53 boundary — 9007199254740993 and 9007199254740992 both round
//     to the same float64, so a float-only comparison would call them equal.
//   - bool/bool, string/string: direct ==.
//   - Anything else (mixed types, including nil) → false.
func EqualScalars(a, b any) bool {
	na, aok := a.(json.Number)
	nb, bok := b.(json.Number)
	if aok && bok {
		if na.String() == nb.String() {
			return true
		}
		if ia, errA := na.Int64(); errA == nil {
			if ib, errB := nb.Int64(); errB == nil {
				return ia == ib
			}
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
