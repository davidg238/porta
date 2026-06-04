// Copyright (c) 2026 Ekorau LLC

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
