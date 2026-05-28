package store

import (
	"strings"
	"testing"
)

func TestNodeNameForDeterministic(t *testing.T) {
	a := NodeNameFor("aabbccddeeff")
	b := NodeNameFor("aabbccddeeff")
	if a != b {
		t.Errorf("not deterministic: %q vs %q", a, b)
	}
	if !strings.Contains(a, "-") {
		t.Errorf("expected adjective-noun shape, got %q", a)
	}
}

func TestNodeNameForDiffersAcrossMacs(t *testing.T) {
	if NodeNameFor("aabbccddeeff") == NodeNameFor("001122334455") {
		t.Error("different MACs should (very likely) differ")
	}
}

// Cross-check vector: jolly-pine's real MAC (30aea41a6208) was named
// "jolly-pine" by the Toit gateway. The Go port must agree byte-for-byte.
func TestNodeNameForMatchesToitGateway(t *testing.T) {
	if got := NodeNameFor("30aea41a6208"); got != "jolly-pine" {
		t.Errorf("NodeNameFor(jolly-pine MAC) = %q, want jolly-pine", got)
	}
}
