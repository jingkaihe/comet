package cli

import (
	"fmt"
	"os"

	"github.com/jingkaihe/comet/internal/version"
	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comet",
		Short: "A tiny Go web terminal",
		Long:  "Comet is a small Go web terminal server with tabs and panes.",
	}

	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "comet %s\ncommit: %s\nbuilt: %s\n", version.Version, version.GitCommit, version.BuildTime)
		},
	})

	return cmd
}

func Execute() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
