package main

import (
	"github.com/spf13/cobra"

	"github.com/agentre-hub/agentre/internal/cli/claudecodecmd"
)

// newClaudeCodeCmd is an internal hook passthrough used by spawned claudecode
// subprocesses. All args after `claudecode` are forwarded verbatim — cobra
// flag parsing is disabled so `--foo` bound for claudecode isn't intercepted.
func newClaudeCodeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "claudecode [args...]",
		Short:              "Internal: claudecode hook passthrough",
		DisableFlagParsing: true,
		Run: func(_ *cobra.Command, args []string) {
			claudecodecmd.Main(args)
		},
	}
}
