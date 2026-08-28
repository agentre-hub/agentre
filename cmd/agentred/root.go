package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// usageError marks an error as caused by bad CLI usage (missing args, unknown
// subcommand, invalid flag value) so main() can map it to exit code 2 while
// runtime errors map to 1.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func newUsageError(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "agentred",
		Short:   "Headless agent compute daemon",
		Version: agentredBuildIdentity(),
		Long: `agentred — headless agent compute daemon.

Stateless executor that runs claude-code / codex subprocesses on behalf of
remote desktops over a binary Protobuf RPC control API on WebSocket.`,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.SetVersionTemplate("agentred {{.Version}}\n")
	root.AddCommand(
		newRunCmd(),
		newStatusCmd(),
		newPairCmd(),
		newLoginCmd(),
		newUnclaimCmd(),
		newLLMCmd(),
		newClaudeCodeCmd(),
		newServiceCmd(),
	)
	return root
}
