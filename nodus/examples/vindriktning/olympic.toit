// device/olympic.toit — the "olympic" trimmed mean used by run-once sampling payloads.
/**
Returns the trimmed mean of $values: drop the single highest and single lowest
  sample, then average the rest. Robust to one spike high and one dropout low.
  Requires at least 3 values so the trim leaves at least one. $values is not mutated.
*/
olympic-mean values/List -> float:
  if values.size < 3: throw "olympic-mean needs >= 3 values"
  sorted := values.sort  // Ascending copy (non-destructive).
  middle := sorted[1 .. sorted.size - 1]
  sum := 0.0
  middle.do: sum += it
  return sum / middle.size
