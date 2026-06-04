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

// Checkin derives the gauge from last-seen + poll interval + max-offline.
func Checkin(seenValid bool, lastSeen, pollIntervalS, maxOfflineS, now int64) CheckinState {
	if !seenValid {
		return CheckinState{Online: false, Color: "red", FillPct: 0, Label: "never seen"}
	}
	elapsed := now - lastSeen
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed <= pollIntervalS:
		pct := 0
		if pollIntervalS > 0 {
			pct = int(elapsed * 100 / pollIntervalS)
		}
		remain := pollIntervalS - elapsed
		return CheckinState{
			Online: true, Color: "green", FillPct: pct,
			Label: fmt.Sprintf("every %s · next ~%s", humanizeDur(pollIntervalS), humanizeDur(remain)),
		}
	case elapsed <= maxOfflineS:
		return CheckinState{
			Online: true, Color: "amber", FillPct: 100,
			Label: fmt.Sprintf("overdue %s", humanizeDur(elapsed-pollIntervalS)),
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
