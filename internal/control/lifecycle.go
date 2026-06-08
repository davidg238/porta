// Copyright (c) 2026 Ekorau LLC

package control

import (
	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
)

// Lifecycle is a command's derived delivery state. It is computed at render
// time from gateway-side data only (the command row + the node's cached
// observed config) — there is no node ACK/NACK, so there is no "failed".
type Lifecycle string

const (
	LifecycleQueued    Lifecycle = "queued"    // undelivered, within the expiry window
	LifecycleDelivered Lifecycle = "delivered" // node pulled it; terminal for non-set verbs
	LifecycleConverged Lifecycle = "converged" // set only: observed config now matches desired
	LifecycleExpired   Lifecycle = "expired"   // undelivered past the offline window
)

// LifecycleOf derives the state of one command.
//   - observed is the node's observed config (app→key→value) from
//     ConfigFromObserved — json.Number values, comparable via config.EqualScalars.
//   - offlineS is the node's derived offline threshold (the expiry window).
//   - now is epoch seconds.
//
// Only "set" can reach Converged; every other verb stops at Delivered (no
// observed-state to reconcile against). A command undelivered for at least
// offlineS is Expired — note the node pulls one command per check-in,
// oldest-first, so a command queued deep behind others can read Expired while
// legitimately waiting its turn (accepted; see the spec).
func LifecycleOf(c store.Command, observed map[string]map[string]any, offlineS, now int64) Lifecycle {
	if !c.DeliveredAt.Valid {
		if now-c.IssuedAt >= offlineS {
			return LifecycleExpired
		}
		return LifecycleQueued
	}
	if c.Verb == "set" {
		if app, key, val, ok := config.DecodeSetArgs(c.Args); ok {
			if o, present := observed[app][key]; present && config.EqualScalars(val, o) {
				return LifecycleConverged
			}
		}
	}
	return LifecycleDelivered
}
