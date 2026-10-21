// Command maktaba-server is the unified Maktaba entry point: one binary
// that supervises the API, streaming, and pipeline services, serves the
// embedded web UI, and carries every CLI subcommand an installer needs
// (setup, update, uninstall, migrate, adduser, keys, models).
//
// It deliberately owns lifecycle + configuration only — the heavy
// service logic lives in the existing api/streaming/pipeline codebases,
// which this binary launches as managed child processes driven by a
// single server.toml (see internal/supervisor + internal/config).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra already prints the error; exit non-zero for scripts.
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "maktaba-server",
		Short:         "Maktaba unified server + installer CLI",
		Long:          rootLong,
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version.String(),
		// Epic 36 story 36.2: a Mac install made before the Mac paths moves into
		// ~/Library/Application Support/Maktaba once, before any command reads it.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			from, err := config.MigrateMacDirs()
			if err != nil {
				return fmt.Errorf("move this install to %s: %w", config.LocalDirs().Config, err)
			}
			if from != "" {
				fmt.Fprintf(os.Stderr, "Moved this install from %s to %s\n", from, config.LocalDirs().Config)
			}
			return nil
		},
	}
	// Print our richer version line for `--version`.
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(
		newServeCmd(),
		newSetupCmd(),
		newUpdateCmd(),
		newUninstallCmd(),
		newModelsCmd(),
		newMigrateCmd(),
		newAddUserCmd(),
		newKeysCmd(),
		newMigrateFromDevCmd(),
	)
	return root
}

const rootLong = `maktaba-server runs all of Maktaba from a single binary.

  maktaba-server setup     First-run wizard: write config, run migrations
  maktaba-server serve     Start the API, streaming, pipeline, and web UI
  maktaba-server update    Self-update to the latest release
  maktaba-server uninstall Guided teardown
  maktaba-server models    Manage Whisper transcription models
  maktaba-server migrate   Run database migrations
  maktaba-server adduser   Seed the first admin user
  maktaba-server keys      Generate or rotate JWT signing keys
  maktaba-server migrate-from-dev  Move the developer stack into Maktaba.app

Configuration lives in server.toml (see 'maktaba-server setup').`
