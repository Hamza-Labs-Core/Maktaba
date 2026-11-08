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

// newRunnerSandboxRunCmd is how the supervisor starts a first-party plugin runner on
// Windows (story 54.23): it asks the runner for its policy, grants the group's own
// AppContainer profile its folders, and runs it there in a Job Object
// (appcontainer.LaunchRunner). Hidden: the supervisor's, not a person's.
//
//	maktaba-server runner-sandbox-run -group name [-grants file] -- python -m maktaba_plugin_runner args...
func newRunnerSandboxRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "runner-sandbox-run",
		Short:              "Run a first-party plugin runner inside its Windows AppContainer (the supervisor's)",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		Run: func(_ *cobra.Command, args []string) {
			os.Exit(appcontainer.LaunchRunner(args, os.Stderr))
		},
	}
}
