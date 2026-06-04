// Copyright (c) 2026 Ekorau LLC

package store

// Word lists copied verbatim from examples/toit-gateway/names.toit so the Go
// gateway assigns the same friendly name as the Toit gateway for any MAC.
var adjectives = []string{
	"amber", "brave", "calm", "clever", "eager", "fancy", "gentle", "happy",
	"jolly", "keen", "lively", "merry", "noble", "proud", "quiet", "rapid",
	"shiny", "swift", "tidy", "witty",
}

var nouns = []string{
	"antler", "badger", "cedar", "comet", "dune", "ember", "falcon", "grove",
	"harbor", "ibex", "jaguar", "kestrel", "lynx", "maple", "nimbus", "otter",
	"pine", "quartz", "raven", "summit",
}

// NodeNameFor maps a 12-hex-lowercase MAC to a deterministic adjective-noun
// name. Horner hash with multiplier 31, masked to 31 bits, then indexed into
// the two 20-word lists. Collisions are accepted (operator can override).
func NodeNameFor(mac string) string {
	h := 0
	for _, c := range mac {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	adjective := adjectives[h%len(adjectives)]
	noun := nouns[(h/len(adjectives))%len(nouns)]
	return adjective + "-" + noun
}
