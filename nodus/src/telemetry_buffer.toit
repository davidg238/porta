// device/telemetry_buffer.toit — a bounded in-RAM ring of telemetry entries for
// one wake window. Lives in the spawned telemetry provider process; the supervisor
// drains it once per wake before deep-sleep. RAM-only by design: deep-sleep wipes it.
/**
A bounded buffer of telemetry entries (each a Map). At capacity, $add drops the
  oldest entry and counts the drop; $drain returns every buffered entry oldest-first
  (a leading {"kind":"log"} marker noting how many were dropped, if any) and empties
  the buffer.
*/
class TelemetryBuffer:
  cap_/int
  entries_/Deque := Deque
  dropped_/int := 0

  constructor --cap/int=128:
    if cap < 1: throw "INVALID_ARGUMENT"
    cap_ = cap

  /** Appends $entry, dropping the oldest if already at capacity. */
  add entry/Map -> none:
    if entries_.size >= cap_:
      entries_.remove-first
      dropped_++
    entries_.add entry

  /**
  Number of entries currently buffered (excludes the dropped-count marker that
    $drain prepends when drops have occurred).
  */
  size -> int: return entries_.size

  /** Returns all entries (oldest first, after an optional dropped-count marker) and empties the buffer. */
  drain -> List:
    out := []
    if dropped_ > 0:
      out.add {"kind": "log", "text": "telemetry: dropped $dropped_ entries"}
      dropped_ = 0
    while not entries_.is-empty: out.add entries_.remove-first
    return out
