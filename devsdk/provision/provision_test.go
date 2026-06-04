// Copyright (c) 2026 Ekorau LLC

package provision

import (
	"reflect"
	"testing"
)

func TestGatewayPortaConfig(t *testing.T) {
	g := Gateway{Host: "192.168.0.175", Port: 6969}
	got := g.PortaConfig()
	// Nested dotted keys under the "porta" group, matching the nodus supervisor's
	// gateway_config.toit reader and porta tools-toit-design.md §7.
	want := map[string]any{"gateway.host": "192.168.0.175", "gateway.port": 6969}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PortaConfig() = %#v, want %#v", got, want)
	}
}

func TestGatewayKeyConstants(t *testing.T) {
	if PortaConfigKey != "porta" || GatewayHostKey != "gateway.host" || GatewayPortKey != "gateway.port" {
		t.Fatalf("key constants drifted: group=%q host=%q port=%q",
			PortaConfigKey, GatewayHostKey, GatewayPortKey)
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
