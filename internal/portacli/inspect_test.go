package portacli

import (
	"testing"

	"github.com/davidg238/porta/internal/control"
)

func TestRelativeAge(t *testing.T) {
	if got := control.RelativeAge(0, 1000); got != "never" {
		t.Errorf("never-seen → %q", got)
	}
	if got := control.RelativeAge(940, 1000); got != "60s ago" {
		t.Errorf("60s → %q", got)
	}
	if got := control.RelativeAge(1000-3600, 1000); got != "60m ago" {
		t.Errorf("60m → %q", got)
	}
}

func TestAppsFromObserved(t *testing.T) {
	apps, err := control.AppsFromObserved(`{"apps":{"blink":{"crc":7,"runlevel":3}},"config":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "blink" || apps[0].CRC != 7 || apps[0].Runlevel != 3 {
		t.Errorf("apps = %+v", apps)
	}
	if apps, err := control.AppsFromObserved(""); err != nil || len(apps) != 0 {
		t.Errorf("empty observed: %+v %v", apps, err)
	}
}
