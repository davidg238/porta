package provision

import (
	"reflect"
	"testing"
)

func TestGatewayPortaConfig(t *testing.T) {
	g := Gateway{Host: "192.168.0.175", Port: 6969}
	got := g.PortaConfig()
	want := map[string]any{"host": "192.168.0.175", "port": 6969}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PortaConfig() = %#v, want %#v", got, want)
	}
}

func TestParseGatewayHostPort(t *testing.T) {
	g, err := ParseGateway("192.168.0.175:6969", 6969)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Host != "192.168.0.175" || g.Port != 6969 {
		t.Fatalf("got %+v", g)
	}
}

func TestParseGatewayHostOnlyUsesDefaultPort(t *testing.T) {
	g, err := ParseGateway("gw.local", 6969)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Host != "gw.local" || g.Port != 6969 {
		t.Fatalf("got %+v", g)
	}
}

func TestParseGatewayRejectsEmptyAndBadPort(t *testing.T) {
	if _, err := ParseGateway("", 6969); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := ParseGateway("gw:notaport", 6969); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}
