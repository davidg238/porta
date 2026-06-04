// Copyright (c) 2026 Ekorau LLC

package portacli

import "testing"

func TestServerURLPrecedence(t *testing.T) {
	// Flag set → flag wins over env and default.
	serverFlag = "http://flag:1"
	t.Setenv("PORTA_SERVER", "http://env:2")
	if got := serverURL(); got != "http://flag:1" {
		t.Errorf("flag should win: %q", got)
	}

	// Flag empty, env set → env wins over default.
	serverFlag = ""
	if got := serverURL(); got != "http://env:2" {
		t.Errorf("env should win: %q", got)
	}

	// Flag empty, env empty → default.
	serverFlag = ""
	t.Setenv("PORTA_SERVER", "")
	if got := serverURL(); got != "http://localhost:6970" {
		t.Errorf("default should win: %q", got)
	}
}
