// Copyright (c) 2026 Ekorau LLC

package web

import "fmt"

// CheckinState is the render model for the next-check-in gauge. It doubles as
// the online/offline indicator: Color matches the status dot.
type CheckinState struct {
	Online  bool
	Color   string // "green" | "amber" | "red"
	FillPct int    // 0..100
	Label   string
}

// Checkin derives the gauge from last-seen + the node's check-in cadence and the
// derived offline threshold (OfflineMultiplier × cadence).
//
// cadenceS comes from the node's node_config echo (an always-on node reports on
// its own clock, a deep-sleep node on its sleep cap), falling back to the stored
// poll-interval before the first echo. offlineS is 3×cadence. Both are computed
// by the caller from store.Node (EffectiveCadenceS / OfflineThresholdS).
func Checkin(seenValid bool, lastSeen, cadenceS, offlineS, now int64) CheckinState {
	if !seenValid {
		return CheckinState{Online: false, Color: "red", FillPct: 0, Label: "never seen"}
	}
	elapsed := now - lastSeen
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed <= cadenceS:
		pct := 0
		if cadenceS > 0 {
			pct = int(elapsed * 100 / cadenceS)
		}
		remain := cadenceS - elapsed
		return CheckinState{
			Online: true, Color: "green", FillPct: pct,
			Label: fmt.Sprintf("every %s · next ~%s", humanizeDur(cadenceS), humanizeDur(remain)),
		}
	case elapsed <= offlineS:
		return CheckinState{
			Online: true, Color: "amber", FillPct: 100,
			Label: fmt.Sprintf("overdue %s", humanizeDur(elapsed-cadenceS)),
		}
	default:
		return CheckinState{
			Online: false, Color: "red", FillPct: 100,
			Label: fmt.Sprintf("offline (>%s)", humanizeDur(offlineS)),
		}
	}
}

// humanizeDur renders seconds as a compact "5s"/"30m"/"2h" string.
func humanizeDur(s int64) string {
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%dh", s/3600)
	}
}
