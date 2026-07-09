// Command cosm is a language-agnostic package manager (see SPEC.md).
package main

import (
	"os"

	"cosm/internal/cli"
)

// version is populated via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Execute())
}
