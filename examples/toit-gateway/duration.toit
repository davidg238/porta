// Copyright (c) 2026 Ekorau LLC

// gateway/duration.toit — parse jag/artemis-style durations to whole seconds.

/**
Parses a duration $text such as "30s", "5m", "1h", "2d", or a bare integer
  (interpreted as seconds), returning whole seconds.

Throws a descriptive string on an empty value, a non-numeric magnitude, or an
  unknown unit suffix.
*/
parse-duration-s text/string -> int:
  if text == "": throw "invalid duration: "
  last := text[text.size - 1]
  if '0' <= last <= '9':
    return int.parse text --if-error=: throw "invalid duration: $text"
  magnitude := int.parse text[..text.size - 1] --if-error=: throw "invalid duration: $text"
  if last == 's': return magnitude
  if last == 'm': return magnitude * 60
  if last == 'h': return magnitude * 3600
  if last == 'd': return magnitude * 86400
  throw "invalid duration unit: $text"
