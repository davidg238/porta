package portacli

import "os"

// serverFlag holds the value of the persistent --server flag (registered in
// root.go). Empty means "fall back to $PORTA_SERVER, then the default".
var serverFlag string

// serverURL resolves the porta server base URL: --server, then $PORTA_SERVER,
// then http://localhost:6970 (matches serve's default --http-port). Only the 8
// mutating commands consume it; reads stay db-backed.
func serverURL() string {
	if serverFlag != "" {
		return serverFlag
	}
	if env := os.Getenv("PORTA_SERVER"); env != "" {
		return env
	}
	return "http://localhost:6970"
}
