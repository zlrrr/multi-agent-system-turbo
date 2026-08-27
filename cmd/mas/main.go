// Command mas is the MAS-Turbo command-line interface.
package main

import (
	"os"

	"github.com/zlrrr/multi-agent-system-turbo/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
