package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appcontainer"
)

// newPluginsSandboxRunCmd is how the supervisor starts the plugin host on Windows (#710):
// inside its AppContainer and Job Object, after granting it its folders
// (appcontainer.Launch). Hidden: the supervisor's, not a person's.
//
//	maktaba-server plugins-sandbox-run [-memory bytes] [-read dir]... [-write dir]... -- maktaba-plugind args...
func newPluginsSandboxRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "plugins-sandbox-run",
		Short:              "Run the plugin host inside its Windows AppContainer (the supervisor's)",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		Run: func(_ *cobra.Command, args []string) {
			os.Exit(appcontainer.Launch(args, os.Stderr))
		},
	}
}
