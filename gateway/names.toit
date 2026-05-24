// gateway/names.toit — deterministic jag-style auto-names keyed by MAC.

ADJECTIVES_ ::= [
  "amber", "brave", "calm", "clever", "eager", "fancy", "gentle", "happy",
  "jolly", "keen", "lively", "merry", "noble", "proud", "quiet", "rapid",
  "shiny", "swift", "tidy", "witty",
]
NOUNS_ ::= [
  "antler", "badger", "cedar", "comet", "dune", "ember", "falcon", "grove",
  "harbor", "ibex", "jaguar", "kestrel", "lynx", "maple", "nimbus", "otter",
  "pine", "quartz", "raven", "summit",
]

/**
Returns a stable, friendly "adjective-noun" name for the node identified by $mac
  (lowercase hex).

The mapping is deterministic, so the same MAC always yields the same name across
  runs and processes. Collisions are accepted — the gateway is small and the
  operator can override the name via `device name`.
*/
node-name-for mac/string -> string:
  h := 0
  mac.do --runes: | c | h = (h * 31 + c) & 0x7fff_ffff
  adjective := ADJECTIVES_[h % ADJECTIVES_.size]
  noun := NOUNS_[(h / ADJECTIVES_.size) % NOUNS_.size]
  return "$adjective-$noun"
