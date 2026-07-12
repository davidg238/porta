// Copyright (c) 2026 Ekorau LLC

// profile_status.go derives the liveness of a node's armed profile session for
// the CLI/web/API views. It is read-time derivation only — no background sweeper,
// no node round-trip, no new wire field (see docs PROTOCOL coordination brief).
//
// Crux: an armed-but-resultless session goes "stale" only after the node has
// reported *since* arming and then stayed silent past the commanded window. The
// deadline is anchored on that first post-arm report, NOT on started_at — a
// healthy deep-sleep node is profiled on its next wake and submits its blob a
// whole sleep interval after arming, so a started_at clock would cry stale on a
// perfectly good profile.
package control

import "github.com/davidg238/porta/internal/store"

// ProfileStaleGraceS pads the report-anchored deadline to absorb TFTP delivery
// latency and the node profiling shim's poll interval before a session is
// called stale.
const ProfileStaleGraceS = 60

// ProfileState is the derived liveness of a node's profile session.
type ProfileState string

const (
	ProfileNone      ProfileState = ""          // no armed session
	ProfileAwaiting  ProfileState = "awaiting"  // armed; node has not reported since arming
	ProfileRunning   ProfileState = "running"   // armed; within the window (or open/continuous)
	ProfileStale     ProfileState = "stale"     // armed; deadline passed with no result
	ProfileFulfilled ProfileState = "fulfilled" // a result arrived at/after arming
)

// Label is the operator-facing description of a state.
func (s ProfileState) Label() string {
	switch s {
	case ProfileAwaiting:
		return "armed — awaiting node"
	case ProfileRunning:
		return "profiling"
	case ProfileStale:
		return "stale / timed-out — no result"
	case ProfileFulfilled:
		return "result received"
	default:
		return ""
	}
}

// DeriveProfileState classifies an armed session. lastReportAt and lastResultTS
// are epoch seconds, 0 meaning "never". A session is stale iff it is armed, has
// no result since arming, the node has reported since arming, and now exceeds
// lastReportAt + duration_s + grace. duration_s == 0 (open/continuous) has no
// deadline and never auto-stales.
func DeriveProfileState(sess *store.ProfileSession, lastReportAt, lastResultTS, now int64) ProfileState {
	if sess == nil {
		return ProfileNone
	}
	if lastResultTS >= sess.StartedAt {
		return ProfileFulfilled
	}
	if lastReportAt <= sess.StartedAt {
		return ProfileAwaiting // node has not (yet) reported since we armed
	}
	if sess.DurationS <= 0 {
		return ProfileRunning // open/continuous — no deadline
	}
	if now-lastReportAt > sess.DurationS+ProfileStaleGraceS {
		return ProfileStale
	}
	return ProfileRunning
}

// ProfileStatus is a node's armed profile session plus its derived liveness.
// Session is nil and State is ProfileNone when nothing is armed.
type ProfileStatus struct {
	Session *store.ProfileSession
	State   ProfileState
}

// ProfileSessionStatus loads a node's session, its last report time, and its
// newest result, then derives the liveness state.
func ProfileSessionStatus(st *store.Store, id string, now int64) (ProfileStatus, error) {
	sess, err := st.GetProfileSession(id)
	if err != nil {
		return ProfileStatus{}, err
	}
	if sess == nil {
		return ProfileStatus{State: ProfileNone}, nil
	}
	var lastReport int64
	if n, err := st.GetNode(id); err != nil {
		return ProfileStatus{}, err
	} else if n != nil && n.LastReportAt.Valid {
		lastReport = n.LastReportAt.Int64
	}
	lastResult, err := st.LatestProfileResultTS(id)
	if err != nil {
		return ProfileStatus{}, err
	}
	return ProfileStatus{Session: sess, State: DeriveProfileState(sess, lastReport, lastResult, now)}, nil
}
