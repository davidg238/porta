// Package provision renders the gateway-address provisioning that a node-repo
// flash tool (e.g. nodus flash) injects into a device's firmware.config. The
// neutral contract is the firmware.config["porta"] object:
//
//	{"host": <string>, "port": <int>}
//
// which the node's supervisor reads to find its gateway. WiFi is out of scope
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

// PortaConfigKey is the firmware.config key under which the gateway address
// lives: firmware.config["porta"].
const PortaConfigKey = "porta"

// Gateway is a node's gateway address.
type Gateway struct {
	Host string
	Port int
}

// PortaConfig returns the firmware.config["porta"] object for g.
func (g Gateway) PortaConfig() map[string]any {
	return map[string]any{"host": g.Host, "port": g.Port}
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
