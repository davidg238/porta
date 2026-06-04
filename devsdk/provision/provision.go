// Copyright (c) 2026 Ekorau LLC

// Package provision renders the gateway-address provisioning that a node-repo
// flash tool (e.g. nodus flash) injects into a device's firmware.config. The
// neutral contract is the firmware.config["porta"] object, with dotted sub-keys
// under the "porta" group (mirroring how "wifi" nests):
//
//	{"gateway.host": <string>, "gateway.port": <int>}
//
// which the node's supervisor reads to find its gateway (see the nodus
// gateway_config.toit reader and porta tools-toit-design.md §7). WiFi is out of scope
// here (node tools provision it via their own flasher, e.g. jag's --wifi-*
// flags). The injection MECHANISM is the node tool's concern; this package
// fixes only the shape.
//
// A flash tool builds the config entry like so:
//
//	gw, _ := provision.ParseGateway("192.168.0.175:6969", 6969)
//	config[provision.PortaConfigKey] = gw.PortaConfig()
package provision

import (
	"fmt"
	"strconv"
	"strings"
)

// Firmware-config keys for the gateway address. PortaConfigKey is the group
// under which it lives (firmware.config["porta"]); the host/port ride dotted
// sub-keys within that group. These strings are the wire contract — they must
// match the nodus supervisor's gateway_config.toit reader exactly.
const (
	PortaConfigKey = "porta"
	GatewayHostKey = "gateway.host"
	GatewayPortKey = "gateway.port"
)

// Gateway is a node's gateway address.
type Gateway struct {
	Host string
	Port int
}

// PortaConfig returns the firmware.config["porta"] object for g, with the dotted
// sub-keys the nodus supervisor reads.
func (g Gateway) PortaConfig() map[string]any {
	return map[string]any{GatewayHostKey: g.Host, GatewayPortKey: g.Port}
}

// ParseGateway parses "host" or "host:port". When the port is omitted it uses
// defPort. The host must be non-empty and the port (if present) numeric.
// IPv6 literals are out of scope (bench provisioning uses IPv4 / hostnames):
// a bracketed form like "[::1]:6969" splits on the last colon and fails with an
// "invalid gateway port" error rather than parsing.
func ParseGateway(s string, defPort int) (Gateway, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Gateway{}, fmt.Errorf("empty gateway address")
	}
	host, port := s, defPort
	if i := strings.LastIndex(s, ":"); i >= 0 {
		host = s[:i]
		p, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return Gateway{}, fmt.Errorf("invalid gateway port %q: %w", s[i+1:], err)
		}
		port = p
	}
	if host == "" {
		return Gateway{}, fmt.Errorf("empty gateway host in %q", s)
	}
	return Gateway{Host: host, Port: port}, nil
}
