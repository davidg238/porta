// Command porta is the northbound gateway server + operator CLI.
package main

import (
	"fmt"
	"os"

	"github.com/davidg238/porta/internal/portacli"
)

func main() {
	if err := portacli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "porta:", err)
		os.Exit(1)
	}
}
