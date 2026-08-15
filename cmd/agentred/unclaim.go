package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/agentre-ai/agentre/internal/daemon/state"
	"github.com/agentre-ai/agentre/internal/pkg/paths"
)

func newUnclaimCmd() *cobra.Command {
	return newUnclaimCmdWithDeps(paths.AgentredDataDir, daemonIsRunning)
}

func newUnclaimCmdWithDeps(dataDir func() (string, error), daemonRunning func() bool) *cobra.Command {
	return &cobra.Command{
		Use:   "unclaim",
		Short: "Remove this daemon's local account claim",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireNoRunningDaemon(daemonRunning); err != nil {
				return err
			}
			dir, err := dataDir()
			if err != nil {
				return err
			}
			st, err := state.Load(dir)
			if err != nil {
				return err
			}
			st.Unclaim()
			if err := st.Save(); err != nil {
				return fmt.Errorf("save unclaimed state: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Daemon account claim removed.")
			return nil
		},
	}
}
