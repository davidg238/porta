package portacli

import (
	"testing"
)

func TestRelativeAge(t *testing.T) {
	if got := relativeAge(0, 1000); got != "never" {
		t.Errorf("never-seen → %q", got)
	}
	if got := relativeAge(940, 1000); got != "60s ago" {
		t.Errorf("60s → %q", got)
	}
	if got := relativeAge(1000-3600, 1000); got != "60m ago" {
		t.Errorf("60m → %q", got)
	}
}

func TestAppsFromObserved(t *testing.T) {
	apps, err := appsFromObserved(`{"apps":{"blink":{"crc":7,"runlevel":3}},"config":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "blink" || apps[0].CRC != 7 || apps[0].Runlevel != 3 {
		t.Errorf("apps = %+v", apps)
	}
	if apps, err := appsFromObserved(""); err != nil || len(apps) != 0 {
		t.Errorf("empty observed: %+v %v", apps, err)
	}
}
