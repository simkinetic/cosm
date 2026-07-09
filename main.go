// Command cosm is a language-agnostic package manager (see SPEC.md).
package main

import (
	_ "embed"
	"os"

	"cosm/internal/cli"
)

// version is populated via -ldflags "-X main.version=...".
var version = "dev"

// devWorkspaceGuide is written to $COSM_DEPOT/dev/CLAUDE.md by `cosm setup` so
// Claude Code (and other agents) know how to drive cosm in a develop workspace.
//
//go:embed docs/dev-workspace.md
var devWorkspaceGuide string

func main() {
	cli.Version = version
	cli.DevWorkspaceGuide = devWorkspaceGuide
	os.Exit(cli.Execute())
}
