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
//       report finds it pending and skips, capping re-issues at one per failed
//       report).
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
