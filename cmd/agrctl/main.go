// Command agrctl is Agentre's companion CLI — a small standalone binary that
// owns the `claudecode` PostToolUse hook shim and the `ctl` control commands.
// It exists so the Claude Code hook (fired on every tool use) and terminal
// `ctl` usage don't have to exec the full desktop app binary. It imports only
// internal/cli/* (no bootstrap/app/wails), so it stays tiny and fast to spawn.
package main

import (
	"fmt"
	"os"

	"github.com/agentre-hub/agentre/internal/cli/acpcmd"
	"github.com/agentre-hub/agentre/internal/cli/claudecodecmd"
	"github.com/agentre-hub/agentre/internal/cli/ctlcmd"
)

const usageText = `agrctl — Agentre companion CLI

Usage:
  agrctl ctl <command> [flags]        control a running Agentre desktop (agents/projects/send)
  agrctl acp --agent <name>           run as an ACP v1 agent over stdio (for the acp backend)
  agrctl claudecode hook post-tool    (internal) Claude Code PostToolUse hook helper`

func main() {
	if len(os.Args) < 2 {
		_, _ = fmt.Fprintln(os.Stderr, usageText)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "claudecode":
		claudecodecmd.Main(os.Args[2:]) // calls os.Exit
	case "ctl":
		ctlcmd.Main(os.Args[2:]) // calls os.Exit
	case "acp":
		acpcmd.Main(os.Args[2:]) // calls os.Exit
	default:
		_, _ = fmt.Fprintf(os.Stderr, "agrctl: unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}
