// Command agrctl is Agentre's companion CLI — a small standalone binary that
// owns the `claudecode` PostToolUse hook shim, `acp`, and the top-level control
// verbs (list / get / create / update / delete / help / send). It exists so the
// Claude Code hook (fired on every tool use) and terminal control usage don't
// have to exec the full desktop app binary. It imports only internal/cli/* (no
// bootstrap/app/wails), so it stays tiny and fast to spawn.
package main

import (
	"os"

	"github.com/agentre-hub/agentre/internal/cli/acpcmd"
	"github.com/agentre-hub/agentre/internal/cli/claudecodecmd"
	"github.com/agentre-hub/agentre/internal/cli/ctlcmd"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "claudecode":
			claudecodecmd.Main(os.Args[2:]) // calls os.Exit
		case "acp":
			acpcmd.Main(os.Args[2:]) // calls os.Exit
		}
	}
	// Everything else — including no arguments and unknown commands — is the
	// control CLI's to answer (usage, exit 2).
	ctlcmd.Main(os.Args[1:]) // calls os.Exit
}
