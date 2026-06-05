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

// Checkin derives the gauge from last-seen + check-in interval + max-offline.
//
// The expected cadence is the node's self-reported reportIntervalS when present
// (>0) — an always-on node reports on its own clock, not the poll-interval — and
// falls back to pollIntervalS otherwise. Without this the gauge mis-flags
// always-on nodes "overdue" for the half of every window past poll-interval
// (issue #14).
func Checkin(seenValid bool, lastSeen, pollIntervalS, reportIntervalS, maxOfflineS, now int64) CheckinState {
	if !seenValid {
		return CheckinState{Online: false, Color: "red", FillPct: 0, Label: "never seen"}
	}
	intervalS := pollIntervalS
	if reportIntervalS > 0 {
		intervalS = reportIntervalS
	}
	elapsed := now - lastSeen
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed <= intervalS:
		pct := 0
		if intervalS > 0 {
			pct = int(elapsed * 100 / intervalS)
		}
		remain := intervalS - elapsed
		return CheckinState{
			Online: true, Color: "green", FillPct: pct,
			Label: fmt.Sprintf("every %s · next ~%s", humanizeDur(intervalS), humanizeDur(remain)),
		}
	case elapsed <= maxOfflineS:
		return CheckinState{
			Online: true, Color: "amber", FillPct: 100,
			Label: fmt.Sprintf("overdue %s", humanizeDur(elapsed-intervalS)),
		}
	default:
		return CheckinState{
			Online: false, Color: "red", FillPct: 100,
			Label: fmt.Sprintf("offline (>%s)", humanizeDur(maxOfflineS)),
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
